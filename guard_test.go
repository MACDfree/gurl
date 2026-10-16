package main

import (
	"net/http"
	"net/http/httptest"
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
