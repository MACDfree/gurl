package main

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// localGuard 只让本机页面和 -allow-origin 列出的来源调用 /api/*。
//
// 服务只绑回环，但本机任意进程、任意网页都能打到 127.0.0.1：没有这层检查，一个恶意
// 网页就足以把 gurl 变成「可发任意 HTTP 请求、还会写用户配置文件」的中继。
//
// Host 校验挡 DNS rebinding（evil.com 解析到 127.0.0.1 时 Host 就不是回环名了）；
// Origin 校验挡跨源页面，放行任意端口是因为 dev 模式下浏览器的 Origin 是 Vite 端口
// 而请求落在 Go 端口。
//
// allow 是用户显式声明的额外主机名（-allow-origin）：前后端分机部署后，浏览器带的
// Host/Origin 是实际访问的域名或 IP，不在回环里。两份校验都按主机名匹配 allow，端口
// 不参与比较，所以反代保不保留 Host 都能用。
//
// 「写端点必须收 JSON」不是代码风格，是 CSRF 防线：application/json 请求体强制预检，
// 加上这里零 CORS 头即失败关闭；改协议时别把这个性质改没了。
func localGuard(bindHost string, allow []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 绑定到通配地址是用户主动开放局域网访问，此时 Host 检查无意义。
		if !isUnspecified(bindHost) && !nameTrusted(r.Host, allow) && hostname(r.Host) != bindHost {
			writeFail(w, http.StatusForbidden, "非法 Host")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !nameTrusted(u.Hostname(), allow) {
				writeFail(w, http.StatusForbidden, "非法 Origin")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// parseAllow 解析 -allow-origin 的值：逗号分隔，条目可以是域名/IP（可带端口）或完整
// URL，统一收敛成小写主机名；协议、端口、路径都不参与匹配。
func parseAllow(s string) []string {
	var out []string
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "://") {
			u, err := url.Parse(entry)
			if err != nil {
				continue
			}
			entry = u.Hostname()
		} else {
			entry = hostname(entry)
		}
		if entry = strings.ToLower(entry); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// nameTrusted 判断 host[:port] 是回环名，还是命中 -allow-origin 白名单。
func nameTrusted(hostPort string, allow []string) bool {
	if loopbackHost(hostPort) {
		return true
	}
	h := strings.ToLower(hostname(hostPort))
	if h == "" {
		return false
	}
	for _, a := range allow {
		if h == a {
			return true
		}
	}
	return false
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
