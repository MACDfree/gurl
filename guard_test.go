package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func apiWith(t *testing.T, h http.Handler, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return callWith(t, h, http.MethodGet, "/api/history", "", headers)
}

// /api/* 不要求任何凭据：保护面收敛到 localGuard 的 Host/Origin 校验。
func TestAPINeedsNoCredentials(t *testing.T) {
	h := newTestServer(t)
	if rec := apiWith(t, h, nil); rec.Code != http.StatusOK {
		t.Errorf("不带任何头应 200，实际 %d", rec.Code)
	}
}

func TestOriginAndHost(t *testing.T) {
	h := newTestServer(t)

	// dev 模式下浏览器 Origin 是 Vite 端口、请求落在 Go 端口，所以只看主机是否回环，端口放行。
	if rec := apiWith(t, h, map[string]string{"origin": "http://localhost:9245"}); rec.Code != http.StatusOK {
		t.Errorf("回环 Origin 任意端口应放行，实际 %d", rec.Code)
	}
	if rec := apiWith(t, h, map[string]string{"origin": "http://evil.example"}); rec.Code != http.StatusForbidden {
		t.Errorf("外源 Origin 应 403，实际 %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "http://evil.example/api/history", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("DNS rebinding 形态的 Host 应 403，实际 %d", rec.Code)
	}
}

func TestAllowOrigin(t *testing.T) {
	h := newTestServerWith(t, parseAllow("gurl.example.com, http://192.168.1.5:9246"))

	// 反代保留了 Host：浏览器访问的域名同时出现在 Host 和 Origin 里。
	req := httptest.NewRequest(http.MethodGet, "http://gurl.example.com/api/history", nil)
	req.Header.Set("Origin", "https://gurl.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("白名单内的 Host + Origin 应放行，实际 %d", rec.Code)
	}

	// 直开后端地址：Origin 是局域网 IP；端口不参与匹配。
	if rec := apiWith(t, h, map[string]string{"origin": "http://192.168.1.5:9999"}); rec.Code != http.StatusOK {
		t.Errorf("白名单 IP 任意端口应放行，实际 %d", rec.Code)
	}

	// 白名单按主机名精确匹配，相邻 IP/别的域名照旧 403。
	if rec := apiWith(t, h, map[string]string{"origin": "http://192.168.1.6:9246"}); rec.Code != http.StatusForbidden {
		t.Errorf("白名单外的 Origin 应 403，实际 %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "http://other.example/api/history", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("白名单外的 Host 应 403，实际 %d", rec.Code)
	}
}

func TestParseAllow(t *testing.T) {
	got := parseAllow(" Gurl.Example.COM , http://a.test:8080 , https://b.test/x , , ")
	want := []string{"gurl.example.com", "a.test", "b.test"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseAllow = %v, want %v", got, want)
	}
}
