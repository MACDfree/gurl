package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"
)

// envelope 是所有 /api/* 的统一响应形状。
//
// 服务层错误返回 500 + ok:false（不是 200 + ok:false），这样 api.ts 只有一条失败分支，
// 前端才敢把原先 5 处 /* 浏览器预览 */ 静默 catch 全删掉。
type envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data"`
	Error string `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// SetEscapeHTML(false)：默认转义会把响应体里的 <>& 变成 \u003c，2MB HTML 会涨到 3MB；
	// 这份输出只被 JSON.parse 消费，不会内联进 <script>，关掉转义是纯赚。
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, envelope{OK: true, Data: data})
}

func writeFail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, envelope{OK: false, Error: msg})
}

func serviceErr(w http.ResponseWriter, err error) {
	writeFail(w, http.StatusInternalServerError, err.Error())
}

func decodeJSON[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	err := json.NewDecoder(r.Body).Decode(&v)
	switch {
	case err == nil || errors.Is(err, io.EOF):
		return v, true
	case errors.As(err, new(*http.MaxBytesError)):
		// 不特判的话超领会被当成「非法 JSON」报 400，指向完全错误的方向。
		writeFail(w, http.StatusRequestEntityTooLarge, "请求体过大")
		return v, false
	default:
		writeFail(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return v, false
	}
}

// streamSend 把一次请求写成 NDJSON：一帧一行，写完立刻 Flush。
//
// 状态码在这里不够用：错误可能发生在已经发出去三帧之后，那时响应头早写定了。所以只有
// 「一帧都没发出去」才回 writeFail，否则只能把错误塞进 error 帧——前端保留已经显示的
// 事件列表，在下面补一行红字。
func streamSend(w http.ResponseWriter, r *http.Request, svc *HTTPService, req *HTTPRequest) {
	// 反代（nginx 默认开响应缓冲）会把小帧攒成一块再发，流式在浏览器那头就没了；
	// 这个头是专门给它的。
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		// 不能 Flush 就别说自己是流式：帧会全压在写缓冲里，直到请求结束才吐出来，
		// 用户看到的是「连上了，然后一直转圈」。
		writeFail(w, http.StatusInternalServerError, "响应写入器不支持 Flush，流式不可用")
		return
	}
	var started bool
	emit := func(f streamFrame) error {
		started = true
		if err := enc.Encode(f); err != nil {
			return err // 浏览器断开：错误冒泡到 sendStream，上游连接跟着关
		}
		flusher.Flush()
		return nil
	}
	_, err := svc.SendStream(r.Context(), req, emit)
	switch {
	case err == nil:
	case !started:
		writeFail(w, http.StatusInternalServerError, err.Error())
	default:
		if writeErr := emit(streamFrame{Type: "error", Error: err.Error()}); writeErr != nil {
			log.Printf("流式请求 %s 失败且无法回报浏览器: %v", req.URL, err)
		}
	}
}

// newAPIMux 注册业务端点。
//
// 末尾的 "/api/" 兜底是必需的：根 mux 上挂着 SPA 的 /{path...} 兜底，若不在这里挡掉，
// 写错的 /api/xxx 会返回 index.html，浏览器报 "Unexpected token '<' in JSON"，
// 排查方向会被彻底带偏。（ServeMux 按最长匹配，所以它只在没有更具体模式命中时生效。）
func newAPIMux(svc *HTTPService) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/send", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeJSON[*HTTPRequest](w, r)
		if !ok {
			return
		}
		resp, err := svc.Send(req)
		if err != nil {
			serviceErr(w, err)
			return
		}
		writeOK(w, resp)
	})

	mux.HandleFunc("POST /api/stream", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeJSON[*HTTPRequest](w, r)
		if !ok {
			return
		}
		streamSend(w, r, svc, req)
	})

	mux.HandleFunc("POST /api/pick-file", func(w http.ResponseWriter, r *http.Request) {
		path, err := svc.PickFile()
		if err != nil {
			serviceErr(w, err)
			return
		}
		writeOK(w, path)
	})

	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.GetHistory()
		if err != nil {
			serviceErr(w, err)
			return
		}
		writeOK(w, list)
	})

	mux.HandleFunc("DELETE /api/history", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.ClearHistory(); err != nil {
			serviceErr(w, err)
			return
		}
		writeOK(w, nil)
	})

	mux.HandleFunc("GET /api/env", func(w http.ResponseWriter, r *http.Request) {
		state, err := svc.GetEnvState()
		if err != nil {
			serviceErr(w, err)
			return
		}
		writeOK(w, state)
	})

	mux.HandleFunc("PUT /api/env", func(w http.ResponseWriter, r *http.Request) {
		state, ok := decodeJSON[EnvState](w, r)
		if !ok {
			return
		}
		if err := svc.SaveEnvState(state); err != nil {
			serviceErr(w, err)
			return
		}
		writeOK(w, nil)
	})

	mux.HandleFunc("GET /api/cookies", func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.GetCookies()
		if err != nil {
			serviceErr(w, err)
			return
		}
		writeOK(w, list)
	})

	mux.HandleFunc("PUT /api/cookies", func(w http.ResponseWriter, r *http.Request) {
		records, ok := decodeJSON[[]CookieRecord](w, r)
		if !ok {
			return
		}
		if err := svc.SaveCookies(records); err != nil {
			serviceErr(w, err)
			return
		}
		writeOK(w, nil)
	})

	mux.HandleFunc("GET /api/collections", func(w http.ResponseWriter, r *http.Request) {
		nodes, err := svc.GetCollections()
		if err != nil {
			serviceErr(w, err)
			return
		}
		writeOK(w, nodes)
	})

	mux.HandleFunc("PUT /api/collections", func(w http.ResponseWriter, r *http.Request) {
		nodes, ok := decodeJSON[[]*CollectionNode](w, r)
		if !ok {
			return
		}
		if err := svc.SaveCollections(nodes); err != nil {
			serviceErr(w, err)
			return
		}
		writeOK(w, nil)
	})

	mux.HandleFunc("GET /api/response/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		cache := bodyCacheRef()
		f, err := cache.Open(id)
		if err != nil {
			writeFail(w, http.StatusNotFound, "完整响应已过期或不存在")
			return
		}
		defer f.Close()

		h := w.Header()
		// 缓存里是上游（=攻击者可影响）的字节，又从我这个 origin 发出去。照搬上游
		// Content-Type 就等于在本机挂了一个同源存储型 HTML——本机 origin 天然过
		// Host/Origin 校验，这页 HTML 就能拿 /api/send 当跳板。类型与处置一律自己定。
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", `attachment; filename="`+cache.Name(id)+`"`)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'")
		http.ServeContent(w, r, "", time.Time{}, f)
	})

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeFail(w, http.StatusNotFound, "未知接口")
	})

	return mux
}
