package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixedServer(t *testing.T, contentType string, fill byte, total int) *httptest.Server {
	t.Helper()
	payload := make([]byte, total)
	if fill == 0 { // 用多字节字符填满，制造「截断点落在半个字符上」的情形
		copy(payload, []byte(strings.Repeat("中", total/3+1))[:total])
	} else {
		for i := range payload {
			payload[i] = fill
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(payload)
	}))
}

func sendVia(t *testing.T, h http.Handler, target string) HTTPResponse {
	t.Helper()
	rec := call(t, h, http.MethodPost, "/api/send",
		fmt.Sprintf(`{"method":"GET","url":%q,"timeoutMs":30000}`, target))
	if rec.Code != http.StatusOK {
		t.Fatalf("send 失败 %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data HTTPResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data
}

// TestSizeReportsTrueTotal 是 Size 那个 bug 的回归守卫：旧实现在截断之后才取
// len(data)，导致 UI 上的大小永远 ≤2MB，用户以为响应就这么大。
func TestSizeReportsTrueTotal(t *testing.T) {
	const total = 6 << 20
	up := fixedServer(t, "application/octet-stream", 'a', total)
	defer up.Close()

	h := newTestServer(t)
	resp := sendVia(t, h, up.URL+"/report.csv")

	if resp.Size != total {
		t.Errorf("Size = %d，期望 %d", resp.Size, total)
	}
	if !resp.Truncated {
		t.Error("应标记 truncated")
	}
	if len(resp.Body) != previewCap {
		t.Errorf("预览长度 = %d，期望 %d", len(resp.Body), previewCap)
	}
	if resp.BodyID == "" {
		t.Error("超限响应应给出 bodyId 供下载")
	}
}

func TestUnderCapUnchanged(t *testing.T) {
	const total = 1 << 10
	up := fixedServer(t, "application/json", 'b', total)
	defer up.Close()

	resp := sendVia(t, newTestServer(t), up.URL)
	if resp.Truncated || resp.BodyID != "" || resp.Size != total || len(resp.Body) != total {
		t.Errorf("未超限的快路径被改坏了: truncated=%v bodyId=%q size=%d body=%d",
			resp.Truncated, resp.BodyID, resp.Size, len(resp.Body))
	}
}

// 截断点落在多字节字符中间时，不能把纯文本误判成二进制（旧代码直接对截断后的
// head 调 utf8.Valid）。
func TestTruncatedTextStaysText(t *testing.T) {
	up := fixedServer(t, "application/octet-stream", 0, 6<<20)
	defer up.Close()

	resp := sendVia(t, newTestServer(t), up.URL)
	if !resp.IsText {
		t.Error("多字节文本在截断处被误判成二进制")
	}
}

func TestDownloadRoute(t *testing.T) {
	const total = 6 << 20
	// 上游故意是 text/html：下载端点必须把它改写成 octet-stream + attachment，
	// 否则就是从本应用 origin 发出的同源存储型 HTML。
	up := fixedServer(t, "text/html", 'a', total)
	defer up.Close()

	h := newTestServer(t)
	resp := sendVia(t, h, up.URL+"/report.html")
	if resp.BodyID == "" {
		t.Fatal("没有 bodyId，无法测下载")
	}

	rec := callWith(t, h, http.MethodGet, "/api/response/"+resp.BodyID, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("下载应 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q，绝不能沿用上游的 text/html", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "gurl-report.html") {
		t.Errorf("Content-Disposition = %q", got)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("缺 nosniff")
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("缺 CSP")
	}
	if n := rec.Body.Len(); n != total {
		t.Errorf("下载字节数 = %d，期望 %d", n, total)
	}
}

func TestDownloadRejectsBadID(t *testing.T) {
	h := newTestServer(t)
	for _, id := range []string{"deadbeef", strings.Repeat("A", 32), strings.Repeat("z", 32)} {
		rec := callWith(t, h, http.MethodGet, "/api/response/"+id, "", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("id %q 应 404，实际 %d", id, rec.Code)
		}
	}
	// 含 .. 的路径压根到不了 handler：ServeMux 先把未规范化的路径 307 重定向到清洗后的
	// 地址。这里只要求「拿不到内容」，不锁定具体状态码。
	rec := callWith(t, h, http.MethodGet, "/api/response/../../etc/passwd", "", nil)
	if rec.Code == http.StatusOK {
		t.Error("路径穿越竟然返回了内容")
	}
}

func TestOverHardCap(t *testing.T) {
	const total = 65 << 20
	up := fixedServer(t, "application/octet-stream", 'a', total)
	defer up.Close()

	resp := sendVia(t, newTestServer(t), up.URL)
	if !resp.Truncated {
		t.Error("应标记 truncated")
	}
	if resp.BodyID != "" {
		t.Errorf("超过 hardCap 不该留缓存文件，bodyId=%q", resp.BodyID)
	}
	if resp.Size < hardCap {
		t.Errorf("Size = %d，应至少读到 hardCap %d", resp.Size, hardCap)
	}
}
