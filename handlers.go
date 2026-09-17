package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeFail(w, http.StatusNotFound, "未知接口")
	})

	return mux
}
