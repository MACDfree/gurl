package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// newToken 生成会话密钥。
func newToken() string {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		log.Fatalf("生成 token 失败: %v", err)
	}
	return hex.EncodeToString(b)
}

// auth 校验会话 token。
//
// 为什么要 token：服务只绑回环，但本机任意进程、任意网页都能打到 127.0.0.1。
// 没有密钥时 gurl 就是一个「可发任意 HTTP 请求、还会写用户配置文件」的中继。
//
// 为什么「不加任何 CORS」是安全的：跨源页面只能免预检发「简单请求」，而这里要求
// Authorization 头、或 application/json 请求体、或 PUT/DELETE —— 任意一条都强制
// 预检，零 CORS 头即失败关闭。所以「写端点必须收 JSON」不是风格，是 CSRF 防线。
//
// Origin 与 Host 校验是纵深防御：前者挡掉带自定义头的跨源页，后者挡掉 DNS rebinding
// （evil.com 解析到 127.0.0.1 时 Host 就不是回环名了）。放行任意端口，因为 dev 模式下
// 浏览器的 Origin 是 Vite 端口而请求落在 Go 端口。
func auth(token string, bindHost string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 绑定到通配地址是用户主动开放局域网访问，此时 Host 检查无意义。
		if !isUnspecified(bindHost) && !loopbackHost(r.Host) && hostname(r.Host) != bindHost {
			writeFail(w, http.StatusForbidden, "非法 Host")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !loopbackHost(u.Hostname()) {
				writeFail(w, http.StatusForbidden, "非法 Origin")
				return
			}
		}
		if !validToken(token, r) {
			writeFail(w, http.StatusUnauthorized, "缺少或错误的访问 token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func validToken(token string, r *http.Request) bool {
	provided := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		provided = strings.TrimPrefix(h, "Bearer ")
	}
	if provided == "" && r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/response/") {
		// 浏览器发起的下载挂不了 header；改走 fetch+Blob 又会把整个上限的字节拉进 JS 堆。
		// 所以只有这一个路由允许 token 走 query，且只限 GET。
		provided = r.URL.Query().Get("t")
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(provided)) == 1
}

func hostname(hostPort string) string {
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		return h
	}
	return hostPort
}

func loopbackHost(hostPort string) bool {
	h := hostname(hostPort)
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return strings.HasPrefix(h, "127.")
}

// isUnspecified 判断 -host 是否是通配地址（用户主动开放非回环访问）。
func isUnspecified(host string) bool {
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return true
	}
	return false
}
