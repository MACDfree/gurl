package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// wireFrame 同时留着解析结果和原始那一行：外层 key 集合也是和 types.ts 对过的契约。
type wireFrame struct {
	streamFrame
	raw string
}

// sseServer 起一个「吐两个事件就一直挂着、永不结束」的上游，最接近真实的 AI 流。
// 第二个事件要等 120ms，用来证明第一个事件没有被攒着。
func sseServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("X-Up", "stream")
		fl := w.(http.Flusher)
		fmt.Fprintf(w, ": ping\n\ndata: 第一\n\n")
		fl.Flush()
		select {
		case <-time.After(2 * time.Second):
			fmt.Fprintf(w, "event: token\ndata: 第二\ndata: 续\n\n")
			fl.Flush()
			<-r.Context().Done() // 既不写结尾空行也不关连接，由客户端取消
		case <-r.Context().Done():
		}
	}))
}

// readSSEViaRealServer 走真 HTTP 读前 want 行 NDJSON——ResponseRecorder 测不到 Flush，
// 也就证明不了「每帧立刻给」。返回前把连接全关掉，好让上游 handler 及早退出。
func readSSEViaRealServer(t *testing.T, upstream string, want int) []wireFrame {
	t.Helper()
	return readFramesFrom(t, newTestServer(t), upstream, want)
}

func readFramesFrom(t *testing.T, handler http.Handler, upstream string, want int) []wireFrame {
	t.Helper()
	srv := httptest.NewServer(handler)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/stream",
		strings.NewReader(`{"method":"GET","url":`+fmt.Sprintf("%q", upstream)+`,"timeoutMs":30000}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var frames []wireFrame
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	for len(frames) < want && sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			frames = append(frames, parseFrame(t, line))
		}
	}
	readErr := sc.Err()
	resp.Body.Close()
	srv.Close()
	if readErr != nil && len(frames) < want {
		t.Fatalf("没能在期限内读到 %d 帧（Flush 可能被中间件吞掉了）: %v", want, readErr)
	}
	return frames
}

func parseFrame(t *testing.T, line string) wireFrame {
	t.Helper()
	var f wireFrame
	if err := json.Unmarshal([]byte(line), &f); err != nil {
		t.Fatalf("帧不是合法 JSON: %v\n%s", err, line)
	}
	f.raw = line
	return f
}

// streamViaRecorder 发一次 /api/stream 并把 NDJSON 按行拆开。
func streamViaRecorder(t *testing.T, h http.Handler, target string) ([]wireFrame, *httptest.ResponseRecorder) {
	t.Helper()
	rec := call(t, h, http.MethodPost, "/api/stream",
		fmt.Sprintf(`{"method":"GET","url":%q,"timeoutMs":30000}`, target))
	var frames []wireFrame
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			frames = append(frames, parseFrame(t, line))
		}
	}
	return frames, rec
}

func frameTypes(frames []wireFrame) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, f.Type)
	}
	return out
}

func jsonKeys(t *testing.T, raw string) []string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestStreamFramesArriveWhileUpstreamOpen 是这次改造的正面：上游还挂着、第二个事件
// 还没写，前端就必须已经拿到 meta 和第一个事件。原实现里这两帧要等 2 秒后连接结束
// 才一起出现——而这条连接根本不结束。
func TestStreamFramesArriveWhileUpstreamOpen(t *testing.T) {
	up := sseServer(t)
	defer up.Close()

	frames := readSSEViaRealServer(t, up.URL, 2)
	if len(frames) != 2 {
		t.Fatalf("帧数 = %d，期望 2: %+v", len(frames), frames)
	}
	if got, want := frameTypes(frames), []string{"meta", "event"}; !equalKeys(got, want) {
		t.Errorf("帧序 = %v，期望 %v", got, want)
	}
	if frames[0].Meta == nil || frames[0].Meta.Status != http.StatusOK {
		t.Errorf("meta 帧不对: %+v", frames[0].Meta)
	}
	var hasUp bool
	for _, h := range frames[0].Meta.Headers {
		if h.Key == "X-Up" && h.Value == "stream" {
			hasUp = true
		}
	}
	if !hasUp {
		t.Errorf("meta.headers 里应有 X-Up: %+v", frames[0].Meta.Headers)
	}
	if ev := frames[1].Event; ev == nil || ev.Data != "第一" || ev.Type != "message" {
		t.Errorf("第一个事件 = %+v，期望 data=第一 type=message", ev)
	}
}

// newTestServerLogged 用 -dev 那条链：requestLog 会把 ResponseWriter 包进 statusRecorder。
// 那一层曾经悄悄不再满足 http.Flusher，于是流式只在开发模式下失效、生产一切正常。
func newTestServerLogged(t *testing.T) http.Handler {
	t.Helper()
	resetHistory(t)
	resetEnvCol(t)
	resetBodyCache(t)
	return newServer(&HTTPService{}, testToken, "127.0.0.1", true, testDist())
}

func TestStreamFlushesThroughAccessLogMiddleware(t *testing.T) {
	up := sseServer(t)
	defer up.Close()

	frames := readFramesFrom(t, newTestServerLogged(t), up.URL, 2)
	if got, want := frameTypes(frames), []string{"meta", "event"}; !equalKeys(got, want) {
		t.Fatalf("帧序 = %v，期望 %v", got, want)
	}
	if ev := frames[1].Event; ev == nil || ev.Data != "第一" {
		t.Errorf("第二帧应是第一个事件: %+v", frames[1])
	}
}

// TestStreamSSEFraming 逐条对上 text/event-stream 的解析规则：注释行、多行 data、
// 自定义事件名、id 跨事件保留、以及结尾没写空行的最后一条。
func TestStreamSSEFraming(t *testing.T) {
	const payload = ": keepalive\n\n" +
		"id: 7\r\nevent: token\r\ndata: hello\r\ndata: world\r\n\r\n" +
		"data: 中文\n\n" +
		"data: 最后一句没写空行"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, payload)
	}))
	defer up.Close()

	frames, rec := streamViaRecorder(t, newTestServer(t), up.URL)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	want := []string{"meta", "event", "event", "event", "end"}
	if got := frameTypes(frames); !equalKeys(got, want) {
		t.Fatalf("帧序 = %v，期望 %v\n%+v", got, want, frames)
	}
	events := []SSEEvent{}
	for _, f := range frames {
		if f.Event != nil {
			events = append(events, *f.Event)
		}
	}
	wantEvents := []SSEEvent{
		{ID: "7", Type: "token", Data: "hello\nworld"},
		{ID: "7", Type: "message", Data: "中文"},
		{ID: "7", Type: "message", Data: "最后一句没写空行"},
	}
	for i := range wantEvents {
		if events[i] != wantEvents[i] {
			t.Errorf("事件 %d = %+v，期望 %+v", i, events[i], wantEvents[i])
		}
	}
	// size 是「从网络读到的字节数」，不是拼出来的正文长度：两者差了注释和帧边界。
	if got := frames[len(frames)-1].Size; got != int64(len(payload)) {
		t.Errorf("end 帧 size = %d，期望 %d", got, len(payload))
	}
}

// TestStreamThreeLineEndings 覆盖 \n / \r\n / lone \r 三种换行。lone \r 是这里唯一
// 容易写错的一种：bufio.ScanLines 把它当普通字符，而用 Peek 判断后一个字节会阻塞等
// 上游，把已经读到的事件卡在那里不发。
func TestStreamThreeLineEndings(t *testing.T) {
	for _, sep := range []string{"\n", "\r\n", "\r"} {
		t.Run(strings.ReplaceAll(sep, "\r", "CR"), func(t *testing.T) {
			raw := "data: one" + sep + sep + "data: two" + sep + sep
			var got []string
			size, truncated, err := readSSE(strings.NewReader(raw), func(f streamFrame) error {
				if f.Event != nil {
					got = append(got, f.Event.Data)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if truncated {
				t.Error("不该判成截断")
			}
			if size != int64(len(raw)) {
				t.Errorf("size = %d，期望 %d", size, len(raw))
			}
			if len(got) != 2 || got[0] != "one" || got[1] != "two" {
				t.Errorf("事件 = %v", got)
			}
		})
	}
}

// TestStreamNonSSEStaysBuffered：上游不是 event-stream 时行为不许变——仍是一帧完整的
// HTTPResponse，预览上限、落盘缓存、下载 id 那套照旧。
func TestStreamNonSSEStaysBuffered(t *testing.T) {
	const total = 6 << 20
	up := fixedServer(t, "application/octet-stream", 'a', total)
	defer up.Close()

	frames, _ := streamViaRecorder(t, newTestServer(t), up.URL+"/report.csv")
	if got, want := frameTypes(frames), []string{"response"}; !equalKeys(got, want) {
		t.Fatalf("帧序 = %v，期望 %v", got, want)
	}
	resp := frames[0].Response
	if resp == nil {
		t.Fatal("没有 response 帧")
	}
	if resp.Size != total || !resp.Truncated || resp.BodyID == "" {
		t.Errorf("超限处理与全缓冲路径不一致: size=%d truncated=%v bodyId=%q",
			resp.Size, resp.Truncated, resp.BodyID)
	}
	if got, want := jsonKeys(t, frames[0].raw), []string{"response", "type"}; !equalKeys(got, want) {
		t.Errorf("response 帧外层 key 变了: %v", got)
	}
}

// TestStreamErrorBeforeFirstFrameKeepsStatus：一帧都没发出去时响应头还没写定，
// 必须继续走统一的 envelope + 500，而不是 200 + 错误帧。
func TestStreamErrorBeforeFirstFrameKeepsStatus(t *testing.T) {
	rec := call(t, newTestServer(t), http.MethodPost, "/api/stream", `{"url":"","timeoutMs":30000}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 500", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"ok":false`) || !strings.Contains(got, "URL 不能为空") {
		t.Fatalf("应为 envelope 错误: %s", got)
	}
}

// TestStreamHeaderTimeoutAppliesToHeadersOnly：流式下 timeoutMs 只管「拿到响应头」
// 那一段。上游 300ms 后才写第一个字节，50ms 的期限必须在拿到响应头之前就把请求掐掉。
func TestStreamHeaderTimeoutAppliesToHeadersOnly(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		io.WriteString(w, "data: late\n\n")
	}))
	defer up.Close()

	rec := call(t, newTestServer(t), http.MethodPost, "/api/stream",
		`{"method":"GET","url":"`+up.URL+`","timeoutMs":50}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, "等待响应头超时") {
		t.Fatalf("错误应指向响应头超时: %s", got)
	}
}

// TestStreamUpstreamBreakIsAnError：上游传一半把连接掐了（ErrAbortHandler 不留合法的
// chunked 收尾），必须补一个 error 帧，而不是一个看起来成功的 end。
func TestStreamUpstreamBreakIsAnError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}))
	defer up.Close()

	frames, _ := streamViaRecorder(t, newTestServer(t), up.URL)
	want := []string{"meta", "event", "error"}
	if got := frameTypes(frames); !equalKeys(got, want) {
		t.Fatalf("帧序 = %v，期望 %v\n%+v", got, want, frames)
	}
	if frames[2].Error == "" {
		t.Error("error 帧应带原因")
	}
}

// TestBufferedReadBreakIsAnError 是同一个守卫在全缓冲路径上的样子：/api/send 必须回
// 500。原实现把 io.ReadAll 的 error 整个丢掉，断流因此长得像一个完整响应。
func TestBufferedReadBreakIsAnError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, strings.Repeat("a", 1<<10))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}))
	defer up.Close()

	rec := call(t, newTestServer(t), http.MethodPost, "/api/send",
		`{"method":"GET","url":"`+up.URL+`","timeoutMs":30000}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 500: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, "读取响应体中断") {
		t.Fatalf("错误应说明正文读断: %s", got)
	}
}

// TestStreamWireContract 对应 types.ts 里的 StreamMeta / SSEEvent：TS 侧是手写的，
// 编译器不会替我们对齐 tag。
func TestStreamWireContract(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "id: 1\nevent: x\ndata: y\n\n")
	}))
	defer up.Close()

	frames, _ := streamViaRecorder(t, newTestServer(t), up.URL)
	if got, want := innerKeys(t, frames[0].raw, "meta"),
		[]string{"headers", "status", "statusText"}; !equalKeys(got, want) {
		t.Errorf("streamMeta 字段集变了:\n got %v\nwant %v", got, want)
	}
	if got, want := innerKeys(t, frames[1].raw, "event"),
		[]string{"data", "id", "type"}; !equalKeys(got, want) {
		t.Errorf("SSEEvent 字段集变了:\n got %v\nwant %v", got, want)
	}
}

// innerKeys 取一帧里嵌套对象的 key 集合。
func innerKeys(t *testing.T, raw, key string) []string {
	t.Helper()
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return jsonKeys(t, string(m[key]))
}
