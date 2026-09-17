package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func apiWith(t *testing.T, h http.Handler, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return callWith(t, h, http.MethodGet, "/api/history", "", headers)
}

func TestTokenRequired(t *testing.T) {
	h := newTestServer(t)

	if rec := apiWith(t, h, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("无凭据应 401，实际 %d", rec.Code)
	}
	if rec := apiWith(t, h, map[string]string{"authorization": "Bearer nope"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("错 token 应 401，实际 %d", rec.Code)
	}
	if rec := apiWith(t, h, map[string]string{"authorization": testToken}); rec.Code != http.StatusUnauthorized {
		t.Errorf("裸 token（缺 Bearer 前缀）应 401，实际 %d", rec.Code)
	}
	if rec := apiWith(t, h, map[string]string{"authorization": "Bearer " + testToken}); rec.Code != http.StatusOK {
		t.Errorf("Bearer token 应 200，实际 %d", rec.Code)
	}
}

// token 走 query 目前只留给下载端点（浏览器发起的下载挂不了 header），其余一律不认。
// C5 会给 /api/response/{id} 开这个口子，届时这里要加对应的正向用例。
func TestQueryTokenRejectedOnAPI(t *testing.T) {
	h := newTestServer(t)
	if rec := apiWith(t, h, map[string]string{"authorization": "Bearer " + testToken}); rec.Code != http.StatusOK {
		t.Fatalf("前置条件不成立")
	}
	rec := callWith(t, h, http.MethodGet, "/api/history?t="+testToken, "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("普通 API 用 query token 应 401，实际 %d", rec.Code)
	}
}

func TestOriginAndHost(t *testing.T) {
	h := newTestServer(t)
	ok := map[string]string{"authorization": "Bearer " + testToken}

	// dev 模式下浏览器 Origin 是 Vite 端口、请求落在 Go 端口，所以只看主机是否回环，端口放行。
	if rec := apiWith(t, h, merge(ok, map[string]string{"origin": "http://localhost:9245"})); rec.Code != http.StatusOK {
		t.Errorf("回环 Origin 任意端口应放行，实际 %d", rec.Code)
	}
	if rec := apiWith(t, h, merge(ok, map[string]string{"origin": "http://evil.example"})); rec.Code != http.StatusForbidden {
		t.Errorf("外源 Origin 应 403，实际 %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "http://evil.example/api/history", nil)
	req.Header.Set("authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("DNS rebinding 形态的 Host 应 403，实际 %d", rec.Code)
	}
}

// 静态资源必须免 token：浏览器加载 /assets/*.js 时不会带 Authorization，
// 页面得先能打开，前端才有机会从 URL 里读到 token。
func TestStaticNeedsNoToken(t *testing.T) {
	h := newTestServer(t)
	if rec := callWith(t, h, http.MethodGet, "/", "", map[string]string{"accept": "text/html"}); rec.Code != http.StatusOK {
		t.Errorf("index.html 应免 token，实际 %d", rec.Code)
	}
	if rec := callWith(t, h, http.MethodGet, "/assets/index-abc.js", "", nil); rec.Code != http.StatusOK {
		t.Errorf("打包资源应免 token，实际 %d", rec.Code)
	}
}

func TestNewTokenShape(t *testing.T) {
	a, b := newToken(), newToken()
	if len(a) != 32 || a == b {
		t.Errorf("token 应为 32 位十六进制且互不相同，得到 %q / %q", a, b)
	}
	if strings.ContainsAny(a, "ghijklmnopqrstuvwxyzGHIJKLMNOPQRSTUVWXYZ") {
		t.Errorf("token 应为小写十六进制: %s", a)
	}
}

func merge(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
