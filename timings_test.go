package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestTimings(t *testing.T) {
	resetHistory(t)
	resetEnvCol(t)
	resetCookies(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow":
			time.Sleep(40 * time.Millisecond)
			io.WriteString(w, "ok")
		case "/a":
			http.Redirect(w, r, "/b", http.StatusFound)
		case "/b":
			io.WriteString(w, "final")
		default:
			io.WriteString(w, "ok")
		}
	}))
	defer srv.Close()

	svc := &HTTPService{}
	get := func(path string) *HTTPResponse {
		t.Helper()
		resp, err := svc.send(&HTTPRequest{Method: "GET", URL: srv.URL + path, TimeoutMS: 5000})
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.Timings == nil {
			t.Fatalf("GET %s: timings 为空", path)
		}
		return resp
	}

	// 上游睡 40ms 才回头 → 首字节必须看得见；直连 IP 不解析、无 TLS，这两段就该是 0。
	slow := get("/slow")
	if slow.Timings.TTFBMs < 30 {
		t.Errorf("ttfbMs = %d，正文前睡了 40ms 却没等到", slow.Timings.TTFBMs)
	}
	if slow.Timings.DNSMs != 0 || slow.Timings.TLSMs != 0 {
		t.Errorf("直连 IP 不该有 DNS/TLS：dns=%d tls=%d", slow.Timings.DNSMs, slow.Timings.TLSMs)
	}
	if slow.Timings.ConnectMs == 0 {
		t.Error("首次连接没有记录连接耗时")
	}
	if slow.DurationMS < slow.Timings.TTFBMs {
		t.Errorf("总耗时 %d 小于首字节 %d", slow.DurationMS, slow.Timings.TTFBMs)
	}

	// 两次请求打到同一个 httptest 服务：连接池该让第二次省掉建连。
	second := get("/slow")
	if !second.Timings.Reused {
		t.Error("第二次请求没复用连接（连接池失效或测试环境不保留空闲连接）")
	}

	// 30x 链：跳数要数得出来，且首字节包含整条链。
	redirect := get("/a")
	if redirect.Timings.Hops != 1 {
		t.Errorf("hops = %d，期望 1", redirect.Timings.Hops)
	}
	if redirect.Status != http.StatusOK {
		t.Errorf("跟随后状态码 = %d，期望 200", redirect.Status)
	}
}

// TestStreamTimings 盯的是流式路径的接线：那条路上 httpReq 会在发出去前最后一刻被
// WithContext(cancelCtx) 换掉 context，trace 要是提前绑在请求上就整个丢了，各阶段静默归零。
func TestStreamTimings(t *testing.T) {
	resetHistory(t)
	resetEnvCol(t)
	resetCookies(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for i := 0; i < 2; i++ {
			io.WriteString(w, "data: tick\n\n")
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(30 * time.Millisecond)
		}
	}))
	defer srv.Close()

	var end *Timings
	_, err := (&HTTPService{}).sendStream(context.Background(),
		&HTTPRequest{Method: "GET", URL: srv.URL, TimeoutMS: 5000},
		func(f streamFrame) error {
			if f.Type == "end" {
				end = f.Timings
			}
			return nil
		})
	if err != nil {
		t.Fatalf("sendStream: %v", err)
	}
	if end == nil {
		t.Fatal("end 帧没带上 timings")
	}
	if end.TTFBMs == 0 {
		t.Error("流式路径 ttfbMs = 0：trace 没挂在最终的 context 上")
	}
	if end.BodyMs < 30 {
		t.Errorf("bodyMs = %d，两拍之间各睡 30ms 却没等到", end.BodyMs)
	}
}
