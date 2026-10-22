package main

import (
	"io/fs"
	"log"
	"net/http"
	"runtime/debug"
	"time"
)

// newServer 组装路由与中间件。fsys 由调用方传入（生产是 embed 的 frontend/dist，
// 测试给 fstest.MapFS），这样静态兜底逻辑可在没有构建产物时验证。upd 可为 nil（测试）。
func newServer(svc *HTTPService, bindHost string, allow []string, logRequests bool, fsys fs.FS, upd *Updater) http.Handler {
	root := http.NewServeMux()
	root.Handle("/api/", baseHeaders(recoverJSON(
		requestLog(logRequests,
			noStore(maxReqBody(maxRequestBytes,
				localGuard(bindHost, allow, newAPIMux(svc, upd))))))))
	root.Handle("/", baseHeaders(newStaticHandler(fsys)))
	return root
}

const maxRequestBytes = 16 << 20

// maxReqBody 用 16MiB 而不是响应上限那个量级：PUT /api/collections 每次编辑都全量
// 重发整棵集合树，限太小会在用户的集合刚好变大时以 413 失败，且报错毫无指向性。
func maxReqBody(n int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, n)
		next.ServeHTTP(w, r)
	})
}

// noStore：/api/* 没有任何校验器（无 ETag / Last-Modified），浏览器可能启发式复用
// GET /api/history 的结果，表现为「刚发的请求不在历史里」。
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func baseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func recoverJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v\n%s", rec, debug.Stack())
				// 不把 panic 内容回显给浏览器。
				writeFail(w, http.StatusInternalServerError, "服务内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush 必须显式转发。Flush 不在 http.ResponseWriter 接口里，所以「内嵌一个接口」的
// 包装类型并不满足 http.Flusher——少了这个方法，-dev 一开访问日志，/api/stream 的
// 每一帧就会被压在 net/http 那 2KB 写缓冲里，直到请求结束才一次性吐出去：流式看起来
// 就「不流」了，而且生产模式（不包这一层）完全正常，极难发现。
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func requestLog(enabled bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !enabled {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// 只记 Path 不记 RawURI：URI 里可能带用户自己的敏感查询参数，别替他写进终端日志。
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
	})
}
