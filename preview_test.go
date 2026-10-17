package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 上游回图片 → 即使没超限也整个落盘，preview 路由用真实类型 inline 提供，
// 前端才有东西可渲染（二进制正文本来进不了 DOM）。
func TestPreviewServesImagesInline(t *testing.T) {
	resetHistory(t)
	resetEnvCol(t)
	resetCookies(t)
	resetBodyCache(t)

	png := []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10, 'g', 'u', 'r', 'l'}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer upstream.Close()

	h := newTestServer(t)
	sendBody := `{"method":"get","url":"` + upstream.URL + `","params":[],"headers":[],` +
		`"bodyType":"none","body":"","form":[],"files":[],"bodyGzip":false,"timeoutMs":30000}`
	rec := call(t, h, http.MethodPost, "/api/send", sendBody)
	var sent struct {
		Data struct {
			IsText bool   `json:"isText"`
			BodyID string `json:"bodyId"`
			Size   int64  `json:"size"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Data.IsText {
		t.Error("PNG 被判成文本")
	}
	if sent.Data.BodyID == "" {
		t.Fatal("媒体响应没有 bodyId，前端没东西可预览")
	}

	rec = call(t, h, http.MethodGet, "/api/preview/"+sent.Data.BodyID, "")
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q，期望 image/png", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline;") {
		t.Errorf("Content-Disposition = %q，期望 inline", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
		t.Errorf("CSP = %q，期望兜一层禁脚本", got)
	}
	if rec.Body.String() != string(png) {
		t.Errorf("正文 = %q", rec.Body.String())
	}
}

// 白名单之外的类型（这里是超限落盘的 JSON）即使在 preview 路由上也只能下载：
// 类型不认识就退回 attachment，不给它任何在本机 origin 下渲染的机会。
func TestPreviewRefusesNonMedia(t *testing.T) {
	h := newTestServer(t) // 里面会 resetBodyCache，条目必须在那之后写
	c := bodyCacheRef()
	id, f, err := c.Create("big.json", "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"a":1}`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rec := call(t, h, http.MethodGet, "/api/preview/"+id, "")
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q，期望退回 octet-stream", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Errorf("Content-Disposition = %q，期望 attachment", got)
	}
}

// SVG 能跑脚本，所以不在白名单里；它本身是合法文本，走代码预览，不亏。
func TestPreviewKeepsSVGOutOfLine(t *testing.T) {
	h := newTestServer(t) // 同上：先建服务，再往缓存里放条目
	c := bodyCacheRef()
	id, f, err := c.Create("icon.svg", "image/svg+xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`<svg onload="alert(1)"/>`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rec := call(t, h, http.MethodGet, "/api/preview/"+id, "")
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q：SVG 不该被 inline 提供", got)
	}
}
