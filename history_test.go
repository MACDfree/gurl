package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

func resetHistory(t *testing.T) {
	t.Helper()
	historyPath = filepath.Join(t.TempDir(), "history.json")
	historyOnce = sync.Once{}
	historyMu.Lock()
	historyCache = nil
	historyMu.Unlock()
}

func TestHistory(t *testing.T) {
	resetHistory(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	svc := &HTTPService{}

	svc.Send(&HTTPRequest{Method: "GET", URL: srv.URL})
	svc.Send(&HTTPRequest{Method: "POST", URL: "http://127.0.0.1:1/x", BodyType: "json", Body: `{"a":1}`})
	svc.Send(&HTTPRequest{URL: ""}) // 空 URL 不记录

	entries, err := svc.GetHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].Request.URL != "http://127.0.0.1:1/x" {
		t.Fatalf("newest first expected, got %v", entries[0].Request.URL)
	}
	if entries[0].Error == "" {
		t.Fatal("failed request should record error")
	}
	if entries[1].Request.URL != srv.URL || entries[1].Status != 200 {
		t.Fatalf("success entry wrong: %+v", entries[1])
	}

	// 持久化：模拟重启后重新加载
	historyOnce = sync.Once{}
	historyMu.Lock()
	historyCache = nil
	historyMu.Unlock()
	reloaded, _ := svc.GetHistory()
	if len(reloaded) != 2 {
		t.Fatalf("reload want 2, got %d", len(reloaded))
	}

	if err := svc.ClearHistory(); err != nil {
		t.Fatal(err)
	}
	cleared, _ := svc.GetHistory()
	if len(cleared) != 0 {
		t.Fatalf("clear want 0, got %d", len(cleared))
	}
}
