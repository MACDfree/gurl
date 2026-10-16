package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// 这些 helper 会改包级路径变量并重置 sync.Once，因此不可与 t.Parallel() 共存。
func testDist() fs.FS {
	return fstest.MapFS{
		"index.html":          {Data: []byte("<!DOCTYPE html><title>gurl</title>")},
		"assets/index-abc.js": {Data: []byte("console.log(1)")},
	}
}

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	resetHistory(t)
	resetEnvCol(t)
	resetBodyCache(t)
	return newServer(&HTTPService{}, "127.0.0.1", false, testDist())
}

// call 发一次不带任何额外头的请求。
func call(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return callWith(t, h, method, target, body, nil)
}

func callWith(t *testing.T, h http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = "127.0.0.1:9246"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// dataKeys 返回 envelope.data 的排序后 key 集合。这是唯一能在用户看到 undefined 之前
// 抓到 Go struct tag 改名的检查——TS 侧类型是手写的，编译器不会替我们对齐。
func dataKeys(t *testing.T, body string) []string {
	t.Helper()
	var env struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("响应不是合法 JSON: %v\n%s", err, body)
	}
	if !env.OK {
		t.Fatalf("期望 ok:true，实际 %s", body)
	}
	keys := make([]string, 0, len(env.Data))
	for k := range env.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func equalKeys(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestAPIWireContract(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hi":1}`))
	}))
	defer upstream.Close()

	h := newTestServer(t)
	sendBody := `{"method":"get","url":"` + upstream.URL + `","params":[],"headers":[],` +
		`"bodyType":"none","body":"","form":[],"files":[],"bodyGzip":false,"timeoutMs":30000}`

	rec := call(t, h, http.MethodPost, "/api/send", sendBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("send 状态码 = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := dataKeys(t, rec.Body.String()),
		[]string{"body", "durationMs", "headers", "isText", "size", "status", "statusText", "truncated"}; !equalKeys(got, want) {
		t.Errorf("HTTPResponse 字段集变了:\n got %v\nwant %v", got, want)
	}

	rec = call(t, h, http.MethodGet, "/api/history", "")
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 1 {
		t.Fatalf("Send 之后历史应有 1 条，实际 %d", len(env.Data))
	}
	keys := make([]string, 0, len(env.Data[0]))
	for k := range env.Data[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"durationMs", "error", "request", "size", "status", "time"}; !equalKeys(keys, want) {
		t.Errorf("HistoryEntry 字段集变了:\n got %v\nwant %v", keys, want)
	}

	rec = call(t, h, http.MethodGet, "/api/env", "")
	if got, want := dataKeys(t, rec.Body.String()), []string{"active", "envs"}; !equalKeys(got, want) {
		t.Errorf("EnvState 字段集变了:\n got %v\nwant %v", got, want)
	}

	// 空集合时 GetCollections 返回 nil slice → data 是 null，前端 App.vue 已有 `?? []`。
	rec = call(t, h, http.MethodGet, "/api/collections", "")
	if !strings.Contains(rec.Body.String(), `"data":null`) {
		t.Errorf("期望空集合序列化成 data:null，实际 %s", rec.Body.String())
	}

	for _, step := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/env", `{"active":"","envs":[]}`},
		{http.MethodPut, "/api/collections", `[{"id":"a","name":"c","type":"collection"}]`},
		{http.MethodDelete, "/api/history", ``},
	} {
		rec := call(t, h, step.method, step.path, step.body)
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s 状态码 = %d, body = %s", step.method, step.path, rec.Code, rec.Body.String())
		}
	}
}

func TestErrorEnvelope(t *testing.T) {
	h := newTestServer(t)
	rec := call(t, h, http.MethodPost, "/api/send", `{"url":""}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("服务层错误应返回 500，实际 %d", rec.Code)
	}
	// api.ts 把 error 字符串原样放进 ApiError.message，前端靠它显示中文原因；
	// 改这个字符串要同步改测试。
	if got := rec.Body.String(); !strings.Contains(got, `"ok":false`) || !strings.Contains(got, "URL 不能为空") {
		t.Fatalf("错误 envelope 形状不对: %s", got)
	}
}

func TestMalformedJSONIs400(t *testing.T) {
	h := newTestServer(t)
	rec := call(t, h, http.MethodPut, "/api/env", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("坏 JSON 应 400，实际 %d", rec.Code)
	}
}

// 未知接口必须回 JSON。根 mux 上的 SPA 兜底会吞掉一切路径，若 /api/ 兜底没生效，
// 前端只会看到 "Unexpected token '<' in JSON"，排查方向被彻底带偏。
func TestUnknownAPIIsJSONNotHTML(t *testing.T) {
	h := newTestServer(t)
	rec := call(t, h, http.MethodGet, "/api/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知接口应 404，实际 %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<!DOCTYPE") {
		t.Fatalf("未知接口返回了 HTML 而不是 JSON: %s", rec.Body.String())
	}
}

func TestSPAFallback(t *testing.T) {
	h := newTestServer(t)
	rec := callWith(t, h, http.MethodGet, "/some/route", "", map[string]string{"accept": "text/html"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<!DOCTYPE") {
		t.Fatalf("HTML 请求应回落到 index.html，实际 %d %s", rec.Code, rec.Body.String())
	}
	// 带扩展名的资源不存在时应当干净 404，而不是返回一份 HTML。
	rec = callWith(t, h, http.MethodGet, "/assets/missing.js", "", map[string]string{"accept": "*/*"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("缺失资源应 404，实际 %d", rec.Code)
	}
}

func TestCacheHeaders(t *testing.T) {
	h := newTestServer(t)
	for _, tc := range []struct{ path, want string }{
		{"/", "no-cache"},
		{"/assets/index-abc.js", "immutable"},
	} {
		rec := callWith(t, h, http.MethodGet, tc.path, "", nil)
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, tc.want) {
			t.Errorf("%s 的 Cache-Control = %q，期望含 %q", tc.path, got, tc.want)
		}
	}
	rec := call(t, h, http.MethodGet, "/api/history", "")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("/api/* 必须 no-store，实际 %q", got)
	}
}

func TestMaxRequestBody(t *testing.T) {
	h := newTestServer(t)
	// 必须是合法的 JSON 前缀：否则解析器在第一个字符就报语法错，走 400 分支，
	// 根本碰不到 MaxBytesReader，测的就不是限流了。
	body := `{"active":"` + strings.Repeat("a", 17<<20)
	req := httptest.NewRequest(http.MethodPut, "/api/env", strings.NewReader(body))
	req.Host = "127.0.0.1:9246"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("17MiB 请求体应 413，实际 %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRecoverReturnsEnvelope(t *testing.T) {
	h := recoverJSON(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应转成 500，实际 %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("不得把 panic 内容回显给浏览器: %s", rec.Body.String())
	}
}
