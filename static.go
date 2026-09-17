package main

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// newStaticHandler 托管前端产物，缓存策略按可哈希性分档：
// index.html 必须每次校验（重新编译的二进制要在刷新后被拿到），
// /assets/* 是 Vite 带内容哈希的文件名，可以放心 immutable 一年。
func newStaticHandler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean 掉 .. 之后再取名字，杜绝路径穿越。
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		}
		if fi, err := fs.Stat(fsys, name); err == nil && !fi.IsDir() {
			switch {
			case name == "index.html":
				w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			case strings.HasPrefix(name, "assets/"):
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			default:
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFileFS(w, r, fsys, name)
			return
		}
		// SPA 兜底刻意收窄到「GET + 无扩展名 + 接受 html」：打错的 /assets/x.js
		// 应该干净地 404，而不是返回一份 HTML 让浏览器报 JSON/JS 解析错误。
		if r.Method == http.MethodGet && path.Ext(name) == "" && acceptsHTML(r) {
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			http.ServeFileFS(w, r, fsys, "index.html")
			return
		}
		http.NotFound(w, r)
	})
}

func acceptsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}
