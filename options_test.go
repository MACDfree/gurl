package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 不跟跳转：302 原样交给用户，Location 看得见，hops 为 0。
func TestNoFollowRedirect(t *testing.T) {
	resetHistory(t)
	resetEnvCol(t)
	resetCookies(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/from" {
			w.Header().Set("Location", "/to")
			w.WriteHeader(http.StatusFound)
			return
		}
		io.WriteString(w, "arrived")
	}))
	defer srv.Close()
	svc := &HTTPService{}

	followed, err := svc.send(&HTTPRequest{Method: "GET", URL: srv.URL + "/from", TimeoutMS: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if followed.Body != "arrived" || followed.Timings.Hops != 1 {
		t.Fatalf("默认应当跟随一跳：body=%q hops=%d", followed.Body, followed.Timings.Hops)
	}

	stopped, err := svc.send(&HTTPRequest{Method: "GET", URL: srv.URL + "/from", TimeoutMS: 5000,
		Opts: &Options{NoFollow: true}})
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != http.StatusFound {
		t.Errorf("不跟跳转时状态码 = %d，期望 302", stopped.Status)
	}
	if got := lookupHeaderName(stopped.Headers, "Location"); got == "" {
		t.Error("302 的 Location 没带回来，这个开关就没意义了")
	}
	if stopped.Timings.Hops != 0 {
		t.Errorf("hops = %d，期望 0", stopped.Timings.Hops)
	}
}

// 跳数上限：超了要报错，而不是悄悄停在半路。
func TestMaxRedirects(t *testing.T) {
	resetHistory(t)
	resetEnvCol(t)
	resetCookies(t)
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/h1":
			http.Redirect(w, r, srvURL+"/h2", http.StatusFound)
		case "/h2":
			http.Redirect(w, r, srvURL+"/h3", http.StatusFound)
		default:
			io.WriteString(w, "deep")
		}
	}))
	defer srv.Close()
	srvURL = srv.URL
	svc := &HTTPService{}

	if _, err := svc.send(&HTTPRequest{Method: "GET", URL: srvURL + "/h1", TimeoutMS: 5000,
		Opts: &Options{MaxRedirects: 1}}); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Errorf("超过 1 跳应当报「上限」，实际 %v", err)
	}
	deep, err := svc.send(&HTTPRequest{Method: "GET", URL: srvURL + "/h1", TimeoutMS: 5000,
		Opts: &Options{MaxRedirects: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if deep.Body != "deep" || deep.Timings.Hops != 2 {
		t.Errorf("3 跳上限内应当走完：body=%q hops=%d", deep.Body, deep.Timings.Hops)
	}
}

// 代理真的被用上了：假代理收到的是绝对 URI，这就是走代理的证据。
func TestProxyIsUsed(t *testing.T) {
	resetHistory(t)
	resetEnvCol(t)
	resetCookies(t)
	var seen string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RequestURI // 走代理时这里是完整 URL
		io.WriteString(w, "via-proxy")
	}))
	defer proxy.Close()
	svc := &HTTPService{}

	resp, err := svc.send(&HTTPRequest{Method: "GET", URL: "http://example.invalid/x", TimeoutMS: 5000,
		Opts: &Options{Proxy: proxy.URL}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != "via-proxy" {
		t.Errorf("body = %q，代理没生效？", resp.Body)
	}
	if !strings.HasPrefix(seen, "http://example.invalid") {
		t.Errorf("代理收到的是 %q，不是绝对 URI——说明请求没走代理", seen)
	}

	// 代理写错协议要报错，不能悄悄直连
	if _, err := clientFor(&Options{Proxy: "ftp://127.0.0.1:21"}, time.Second, nil); err == nil ||
		!strings.Contains(err.Error(), "只认") {
		t.Errorf("ftp 代理应当被拒，实际 %v", err)
	}
	// 只写主机:端口 也能用
	if _, err := clientFor(&Options{Proxy: "127.0.0.1:8888"}, time.Second, nil); err != nil {
		t.Errorf("省略 scheme 的代理地址被拒了：%v", err)
	}
}

// 自签证书：默认拒绝，勾了跳过校验才通。
func TestInsecureTLS(t *testing.T) {
	resetHistory(t)
	resetEnvCol(t)
	resetCookies(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "secure-ish")
	}))
	defer srv.Close()
	svc := &HTTPService{}

	if _, err := svc.send(&HTTPRequest{Method: "GET", URL: srv.URL, TimeoutMS: 5000}); err == nil {
		t.Error("自签证书默认应当失败——校验不能被悄悄绕过")
	}
	ok, err := svc.send(&HTTPRequest{Method: "GET", URL: srv.URL, TimeoutMS: 5000,
		Opts: &Options{InsecureTLS: true}})
	if err != nil {
		t.Fatalf("勾了跳过校验还是失败：%v", err)
	}
	if ok.Body != "secure-ish" {
		t.Errorf("body = %q", ok.Body)
	}
}

// 代理地址里可以写 {{var}}，且替换不能写回界面上那份模板。
func TestProxyEnvSubstitution(t *testing.T) {
	resetEnvCol(t)
	resetHistory(t)
	resetCookies(t)
	svc := &HTTPService{}
	if err := svc.SaveEnvState(EnvState{Active: "dev", Envs: []Environment{{
		Name: "dev", Vars: []KV{{Key: "proxyHost", Value: "127.0.0.1:8888", Enabled: true}},
	}}}); err != nil {
		t.Fatal(err)
	}
	req := &HTTPRequest{Method: "GET", URL: "https://api.dev/x",
		Opts: &Options{Proxy: "http://{{proxyHost}}"}}
	if _, _, opts, err := buildRequest(req); err != nil {
		t.Fatal(err)
	} else if opts.Proxy != "http://127.0.0.1:8888" {
		t.Errorf("代理里的变量没替换：%q", opts.Proxy)
	}
	if req.Opts.Proxy != "http://{{proxyHost}}" {
		t.Errorf("替换写回了原对象：%q", req.Opts.Proxy)
	}
}

func TestCloneRequestDetachesOptions(t *testing.T) {
	original := &Options{Proxy: "http://a"}
	clone := cloneRequest(HTTPRequest{URL: "https://x", Opts: original})
	clone.Opts.Proxy = "http://b"
	if original.Proxy != "http://a" {
		t.Error("克隆出来的 Opts 与原对象共享，改一个动两个")
	}
}

func lookupHeaderName(hs []KV, name string) string {
	for _, h := range hs {
		if strings.EqualFold(h.Key, name) {
			return h.Value
		}
	}
	return ""
}
