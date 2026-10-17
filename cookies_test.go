package main

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

func resetCookies(t *testing.T) {
	t.Helper()
	cookiePath = filepath.Join(t.TempDir(), "cookies.json")
	cookieOnce = sync.Once{}
	cookieMu.Lock()
	cookieCache = nil
	cookieJarBase = nil
	cookieMu.Unlock()
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func cookiePairs(cs []*http.Cookie) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name+"="+c.Value)
	}
	sort.Strings(out)
	return out
}

// 核心场景：登录 POST → 302 链的第一跳设了 session，第二跳（以及之后的独立请求）
// 就该自动带上。中间跳的 Set-Cookie 不在最终响应里，只有罐记得住。
func TestCookieCaptureAndCarry(t *testing.T) {
	resetCookies(t)
	resetHistory(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
		http.Redirect(w, r, "/home", http.StatusFound)
	})
	mux.HandleFunc("/home", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("session"); err == nil && c.Value == "abc" {
			_, _ = w.Write([]byte("logged-in"))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := &HTTPService{}
	resp, err := svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/login"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 || resp.Body != "logged-in" {
		t.Fatalf("跳转链内应带上刚捕获的 cookie，得到 %d %q", resp.Status, resp.Body)
	}

	// 独立请求：不经过 /login，仍自动携带。
	resp, err = svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/home"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("后续独立请求应自动携带 cookie，得到 %d", resp.Status)
	}

	list, err := svc.GetCookies()
	if err != nil {
		t.Fatal(err)
	}
	want := CookieRecord{
		Name: "session", Value: "abc", Domain: "127.0.0.1", HostOnly: true,
		Path: "/", Secure: false, Expires: 0, Enabled: true,
	}
	if len(list) != 1 || list[0] != want {
		t.Fatalf("记录的 cookie 形状不对:\n got %+v\nwant %+v", list, want)
	}
}

// 前端实际全走 /api/stream（内部 sendStream），单独覆盖这条路径的 jar 接线。
func TestCookieCaptureViaStreamPath(t *testing.T) {
	resetCookies(t)
	resetHistory(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "s", Value: "1", Path: "/"})
			return
		}
		if c, err := r.Cookie("s"); err == nil && c.Value == "1" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	svc := &HTTPService{}
	noop := func(streamFrame) error { return nil }
	if _, err := svc.SendStream(context.Background(), &HTTPRequest{Method: "GET", URL: srv.URL + "/set"}, noop); err != nil {
		t.Fatal(err)
	}
	resp, err := svc.SendStream(context.Background(), &HTTPRequest{Method: "GET", URL: srv.URL + "/check"}, noop)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("流式路径应携带捕获的 cookie，得到 %d", resp.Status)
	}
}

// 手动设了 Cookie 头时必须精确发送：不叠加罐里的同名 cookie
// （http.Client 默认会 AddCookie 拼在手动值后面，jarFor 用 nil 换掉这个行为）。
func TestManualCookieHeaderSkipsJar(t *testing.T) {
	resetCookies(t)
	resetHistory(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "captured", Path: "/"})
			return
		}
		_, _ = w.Write([]byte(r.Header.Get("Cookie")))
	}))
	defer srv.Close()

	svc := &HTTPService{}
	if _, err := svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/set"}); err != nil {
		t.Fatal(err)
	}

	resp, err := svc.Send(&HTTPRequest{
		Method:  "GET",
		URL:     srv.URL + "/echo",
		Headers: []KV{{Key: "Cookie", Value: "session=manual", Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != "session=manual" {
		t.Fatalf("手动 Cookie 头应精确发送，得到 %q", resp.Body)
	}

	// 不带手动头时罐照常生效，证明上面的差异确实来自手动优先。
	resp, err = svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/echo"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != "session=captured" {
		t.Fatalf("不带手动头时罐应生效，得到 %q", resp.Body)
	}
}

// Max-Age=0 与过去的 Expires 都是删除信号；删掉后不再携带。
func TestCookieDeleteSignals(t *testing.T) {
	resetCookies(t)
	resetHistory(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/set":
			http.SetCookie(w, &http.Cookie{Name: "a", Value: "1", Path: "/"})
		case "/del":
			w.Header().Add("Set-Cookie", "a=; Max-Age=0; Path=/")
		case "/expire":
			w.Header().Add("Set-Cookie", "a=; Expires=Thu, 01 Jan 1970 00:00:00 GMT; Path=/")
		default:
			_, _ = w.Write([]byte(r.Header.Get("Cookie")))
		}
	}))
	defer srv.Close()

	svc := &HTTPService{}
	if _, err := svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/set"}); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.GetCookies(); len(list) != 1 {
		t.Fatalf("捕获后应有 1 条，实际 %d", len(list))
	}

	if _, err := svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/del"}); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.GetCookies(); len(list) != 0 {
		t.Fatalf("Max-Age=0 后记录应被删除，实际 %+v", list)
	}
	resp, _ := svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/echo"})
	if resp.Body != "" {
		t.Fatalf("删除后不应再携带，得到 %q", resp.Body)
	}

	// 再来一轮，用过去的 Expires 删。
	if _, err := svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/set"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/expire"}); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.GetCookies(); len(list) != 0 {
		t.Fatalf("过期 Expires 后记录应被删除，实际 %+v", list)
	}
}

// 用户停用的槽位再被捕获：值照常刷新，Enabled 保留 false（停用是明确意图）。
func TestCookieCaptureKeepsDisabled(t *testing.T) {
	resetCookies(t)
	resetHistory(t)
	version := "v1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: version, Path: "/"})
	}))
	defer srv.Close()

	svc := &HTTPService{}
	if _, err := svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/"}); err != nil {
		t.Fatal(err)
	}
	list, _ := svc.GetCookies()
	list[0].Enabled = false
	if err := svc.SaveCookies(list); err != nil {
		t.Fatal(err)
	}

	version = "v2"
	if _, err := svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/"}); err != nil {
		t.Fatal(err)
	}
	list, _ = svc.GetCookies()
	if len(list) != 1 {
		t.Fatalf("应有 1 条，实际 %+v", list)
	}
	if list[0].Value != "v2" {
		t.Errorf("值应被新响应刷新，得到 %q", list[0].Value)
	}
	if list[0].Enabled {
		t.Error("重新捕获不得把停用的 cookie 静默启用")
	}
}

// 重建的匹配行为必须与 stdlib jar 完全一致：同一批 Set-Cookie 分别喂给真 jar
// （模拟捕获）和「记录 → 重建」路径，对一组探针 URL 断言两边的输出相同。
func TestCookieRebuildMatchesStdlib(t *testing.T) {
	base, _ := cookiejar.New(nil)
	src := mustURL(t, "https://sub.example.com/admin/login")

	rounds := [][]*http.Cookie{
		{
			{Name: "host", Value: "1", Path: "/"},
			{Name: "dom", Value: "2", Domain: ".example.com", Path: "/"},
			{Name: "sec", Value: "3", Domain: "example.com", Path: "/admin", Secure: true},
			{Name: "nolive", Value: "x", Domain: "example.com", Path: "/", MaxAge: -1}, // 删除信号，无此条
		},
		{
			{Name: "dom", Value: "2b", Domain: ".example.com", Path: "/"}, // 覆盖同槽位
			{Name: "host", Value: "1", Path: "/", MaxAge: -1},             // 删除 host-only
		},
	}

	var cache []CookieRecord
	now := time.Now()
	for _, round := range rounds {
		base.SetCookies(src, round)
		for _, c := range round {
			applySetCookie(&cache, src, c, now)
		}
	}
	mine := rebuildJar(cache)

	probes := []string{
		"https://sub.example.com/admin/x",
		"https://sub.example.com/other",
		"https://example.com/admin/x",
		"https://example.com/",
		"http://example.com/admin/x",
		"https://deep.sub.example.com/",
		"https://other.com/",
	}
	for _, p := range probes {
		u := mustURL(t, p)
		want := cookiePairs(base.Cookies(u))
		got := cookiePairs(mine.Cookies(u))
		if !equalKeys(got, want) {
			t.Errorf("%s: 与 stdlib jar 不一致\n got %v\nwant %v", p, got, want)
		}
	}
}

// GetCookies 过滤已过期项；SaveCookies 顺手把过期项从文件里剪掉。
func TestGetCookiesHidesExpired(t *testing.T) {
	resetCookies(t)
	svc := &HTTPService{}
	if err := svc.SaveCookies([]CookieRecord{
		{Name: "live", Value: "1", Domain: "example.com", Path: "/", Enabled: true},
		{Name: "dead", Value: "2", Domain: "example.com", Path: "/", Expires: time.Now().Add(-time.Minute).Unix(), Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	list, _ := svc.GetCookies()
	if len(list) != 1 || list[0].Name != "live" {
		t.Fatalf("过期的应被隐藏，实际 %+v", list)
	}
	cookieMu.Lock()
	cached := len(cookieCache)
	cookieMu.Unlock()
	if cached != 1 {
		t.Fatalf("过期的应被剪掉，缓存里还有 %d 条", cached)
	}
}

// 模拟重启：清运行时状态、保留 cookies.json，重新加载后照常携带。
func TestCookiePersistenceRoundTrip(t *testing.T) {
	resetCookies(t)
	resetHistory(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "s", Value: "1", Path: "/"})
			return
		}
		if c, err := r.Cookie("s"); err == nil && c.Value == "1" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	svc := &HTTPService{}
	if _, err := svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/set"}); err != nil {
		t.Fatal(err)
	}

	cookieOnce = sync.Once{}
	cookieMu.Lock()
	cookieCache = nil
	cookieJarBase = nil
	cookieMu.Unlock()

	resp, err := svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL + "/check"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("重载后应仍携带 cookie，得到 %d", resp.Status)
	}
	if list, _ := svc.GetCookies(); len(list) != 1 {
		t.Fatalf("重载后应有 1 条，实际 %+v", list)
	}
}
