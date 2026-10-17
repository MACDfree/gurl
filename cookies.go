package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Cookie 罐：记录（cookies.json）是唯一真相，cookiejar.Jar 只是它的派生缓存。
//
// 每次捕获到 Set-Cookie 或用户在弹窗里保存后，都把 jar 整体重建。这样捕获与编辑
// 走同一条写入路径，不存在「停了某条 cookie，下次捕获又把它带回来」这类状态漂移；
// 域名/路径/Secure/过期的匹配规则也不用自己实现——重放时交给 stdlib 的 jar 判定。
//
// 捕获发生在「每一跳」：http.Client 对重定向链的每一跳都先调 Jar.Cookies 注入、
// 拿到响应后调 Jar.SetCookies 收存（net/http/client.go 的 c.send），所以登录 POST
// →302→ 首页 这条链里，第一跳设的 session cookie 在第二跳就已带上，全程不需要
// 用户手工复制 Cookie 头。中间跳的 Set-Cookie 不在最终响应里，UI 上本来就看不到，
// 只有罐记得住——这正是这个功能存在的理由。

type CookieRecord struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"` // 解析后的域名，无前导点；domain cookie 与 host-only 共用此列
	// HostOnly = Set-Cookie 未带 Domain 属性：只对精确主机生效，子域不带。
	HostOnly bool   `json:"hostOnly"`
	Path     string `json:"path"`    // 解析后总以 / 开头
	Secure   bool   `json:"secure"`  // 不进 UI，参与重建
	Expires  int64  `json:"expires"` // Unix 秒；0 = 会话 cookie
	Enabled  bool   `json:"enabled"` // 停用 = 重建时跳过
}

var (
	cookieMu      sync.Mutex
	cookieOnce    sync.Once
	cookieCache   []CookieRecord
	cookieJarBase *cookiejar.Jar
	cookiePath    = filepath.Join(configDir(), "cookies.json")
)

func loadCookies() {
	loadJSON(cookiePath, &cookieCache)
	cookieJarBase = rebuildJar(cookieCache)
}

// rebuildJar 从记录全量重建 jar。合成 URL 的构造是重放的关键：
//   - host-only：URL.Host = Domain 且 cookie 不带 Domain 属性 → jar 记为精确主机；
//   - domain cookie：cookie.Domain = Domain → 命中 jar 的 domain 匹配，子域自动携带；
//   - Secure 只决定合成 URL 的 scheme（jar 存 cookie 不看它，发送端的 secureMatch 才看）；
//   - Expires 是捕获时折算好的绝对时间，不带 Max-Age，重启/重建都不会把过期时间往后滑。
func rebuildJar(records []CookieRecord) *cookiejar.Jar {
	j, _ := cookiejar.New(nil)
	for _, r := range records {
		if !r.Enabled || r.Name == "" || r.Domain == "" {
			continue
		}
		scheme := "http"
		if r.Secure {
			scheme = "https"
		}
		u := &url.URL{Scheme: scheme, Host: r.Domain, Path: "/"}
		c := &http.Cookie{Name: r.Name, Value: r.Value, Path: r.Path, Secure: r.Secure}
		if !r.HostOnly {
			c.Domain = r.Domain
		}
		if r.Expires > 0 {
			c.Expires = time.Unix(r.Expires, 0)
		}
		j.SetCookies(u, []*http.Cookie{c})
	}
	return j
}

// captureJar 冒充 http.CookieJar 接口。无状态：真相全在包级变量里，这样两条发请求
// 路径（全缓冲与流式）挂上的是同一个罐。
type captureJar struct{}

func (captureJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	cookieOnce.Do(loadCookies)
	cookieMu.Lock()
	defer cookieMu.Unlock()
	changed := false
	now := time.Now()
	for _, c := range cookies {
		if applySetCookie(&cookieCache, u, c, now) {
			changed = true
		}
	}
	if !changed {
		return
	}
	cookieJarBase = rebuildJar(cookieCache)
	saveCookiesLocked()
}

func (captureJar) Cookies(u *url.URL) []*http.Cookie {
	cookieOnce.Do(loadCookies)
	cookieMu.Lock() // base 指针会被整体替换，裸读就是数据竞争
	defer cookieMu.Unlock()
	return cookieJarBase.Cookies(u)
}

// jarFor 决定这次请求用不用罐：手动设了 Cookie 头则返回 nil（手动优先）。
//
// 不能两样都给：http.Client 会把罐里的 cookie 追加到手动值后面（AddCookie 是拼
// 字符串，request.go），同名会出现两份、由上游决定谁生效。"我手写了 Cookie 头"
// 是明确的意图表达，此时精确发送它才是对的；这也给出「不想要罐」的退出通道。
func jarFor(req *http.Request) http.CookieJar {
	if req.Header.Get("Cookie") != "" {
		return nil
	}
	return captureJar{}
}

// applySetCookie 把一条 Set-Cookie 落进记录，返回是否有变化（无变化不落盘）。
// 归并键 = Name+Domain+Path，与 stdlib jar 内部的 entry.id() 一致。
func applySetCookie(cache *[]CookieRecord, u *url.URL, c *http.Cookie, now time.Time) bool {
	if c.Name == "" {
		return false // 空名 cookie 在展示和重放里都没有意义，直接忽略
	}
	domain, path, hostOnly, ok := resolveSlot(u, c)
	if !ok {
		return false // jar 也会拒绝这条（非法 Domain 等），记录同样不动
	}

	// Max-Age 优先于 Expires；正值折算成绝对时间，避免重放时滑动。
	expires := int64(0)
	remove := false
	switch {
	case c.MaxAge < 0:
		remove = true
	case c.MaxAge > 0:
		expires = now.Add(time.Duration(c.MaxAge) * time.Second).Unix()
	case c.Expires.IsZero():
		expires = 0 // 会话 cookie
	default:
		if !c.Expires.After(now) {
			remove = true
		} else {
			expires = c.Expires.Unix()
		}
	}

	records := *cache
	for i, r := range records {
		if r.Name != c.Name || r.Domain != domain || r.Path != path {
			continue
		}
		if remove {
			*cache = append(records[:i], records[i+1:]...)
			return true
		}
		// 同槽位刷新：值/属性跟随新响应，Enabled 保留用户的选择——
		// 停用是用户对这一条的明确意图，静默翻回来比「值悄悄更新」唐突得多。
		if r.Value == c.Value && r.Secure == c.Secure && r.Expires == expires && r.HostOnly == hostOnly {
			return false
		}
		r.Value = c.Value
		r.Secure = c.Secure
		r.Expires = expires
		r.HostOnly = hostOnly
		records[i] = r
		return true
	}
	if remove {
		return false
	}
	*cache = append(records, CookieRecord{
		Name: c.Name, Value: c.Value, Domain: domain, HostOnly: hostOnly,
		Path: path, Secure: c.Secure, Expires: expires, Enabled: true,
	})
	return true
}

// resolveSlot 复刻 stdlib cookiejar 的槽位解析（canonicalHost / defaultPath /
// domainAndType；net/http/cookiejar jar.go，Go 1.27 语义）。stdlib 没有读回元数据的
// API（jar.Cookies 只返回 Name/Value），所以捕获侧必须自己算出与 jar 一致的槽位。
// ok=false 表示这条会被 jar 拒绝。
//
// 与 stdlib 的已知差异：不做 punycode（toASCII）。非 ASCII 域名的 domain cookie
// 会以原样存下，重建时被 jar 拒绝而静默失效；日常域名全是 punycode，不为此引依赖。
func resolveSlot(u *url.URL, c *http.Cookie) (domain, path string, hostOnly, ok bool) {
	host := canonicalCookieHost(u.Host)
	if host == "" {
		return "", "", false, false
	}
	path = c.Path
	if path == "" || path[0] != '/' {
		path = defaultPath(u.Path)
	}
	if c.Domain == "" {
		return host, path, true, true
	}
	if ipLikeHost(host) {
		// stdlib 对 IP 地址：Domain 必须精确等于 host（前导点也不做宽容），按 host-only 存。
		if host != c.Domain {
			return "", "", false, false
		}
		return host, path, true, true
	}
	d := strings.TrimPrefix(c.Domain, ".")
	if d == "" || d[0] == '.' || d[len(d)-1] == '.' || !isASCII(d) {
		return "", "", false, false
	}
	d = asciiLower(d)
	if host != d && !hasDotSuffix(host, d) {
		return "", "", false, false // www.a.com 不能给别人设 cookie
	}
	return d, path, false, true
}

// canonicalCookieHost：去端口、去尾点、ASCII 小写（stdlib canonicalHost 去掉 toASCII 部分）。
func canonicalCookieHost(hostport string) string {
	host := hostport
	if hasPort(host) {
		var err error
		host, _, err = net.SplitHostPort(host)
		if err != nil {
			return ""
		}
	}
	return asciiLower(strings.TrimSuffix(host, "."))
}

func hasPort(host string) bool {
	colons := strings.Count(host, ":")
	if colons == 0 {
		return false
	}
	if colons == 1 {
		return true
	}
	return host[0] == '[' && strings.Contains(host, "]:")
}

func ipLikeHost(host string) bool {
	if strings.ContainsAny(host, ":%") {
		return true
	}
	return net.ParseIP(host) != nil
}

// defaultPath 是 RFC 6265 §5.1.4 的路径默认值，与 stdlib 同名函数逐行一致。
func defaultPath(path string) string {
	if len(path) == 0 || path[0] != '/' {
		return "/"
	}
	i := strings.LastIndex(path, "/")
	if i == 0 {
		return "/"
	}
	return path[:i]
}

func hasDotSuffix(s, suffix string) bool {
	return len(s) > len(suffix) && s[len(s)-len(suffix)-1] == '.' && s[len(s)-len(suffix):] == suffix
}

// isASCII 对应 stdlib 的 ascii.ToLower 判定：非 ASCII 域名一律拒绝。
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// asciiLower 只折 ASCII 大写，非 ASCII 字节原样保留（stdlib 内部 ascii.ToLower 的语义）。
func asciiLower(s string) string {
	hasUpper := false
	for i := 0; i < len(s); i++ {
		if 'A' <= s[i] && s[i] <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return s
	}
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// GetCookies 返回未过期的记录副本（弹窗展示）。会话 cookie（Expires=0）永不过期。
func (h *HTTPService) GetCookies() ([]CookieRecord, error) {
	cookieOnce.Do(loadCookies)
	cookieMu.Lock()
	defer cookieMu.Unlock()
	now := time.Now().Unix()
	var out []CookieRecord
	for _, r := range cookieCache {
		if r.Expires > 0 && r.Expires <= now {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// SaveCookies 全量替换记录（弹窗保存）。顺手剪掉已过期项，文件不积垃圾。
func (h *HTTPService) SaveCookies(records []CookieRecord) error {
	cookieOnce.Do(loadCookies)
	cookieMu.Lock()
	defer cookieMu.Unlock()
	now := time.Now().Unix()
	kept := make([]CookieRecord, 0, len(records))
	for _, r := range records {
		if r.Expires > 0 && r.Expires <= now {
			continue
		}
		kept = append(kept, r)
	}
	cookieCache = kept
	cookieJarBase = rebuildJar(cookieCache)
	saveCookiesLocked()
	return nil
}

func saveCookiesLocked() {
	data, err := json.Marshal(cookieCache)
	if err != nil {
		return
	}
	_ = atomicWrite(cookiePath, data)
}
