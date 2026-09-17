package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func resetEnvCol(t *testing.T) {
	t.Helper()
	envPath = filepath.Join(t.TempDir(), "env.json")
	envOnce = sync.Once{}
	envMu.Lock()
	envCache = EnvState{}
	envMu.Unlock()

	colPath = filepath.Join(t.TempDir(), "col.json")
	colOnce = sync.Once{}
	colMu.Lock()
	colCache = nil
	colMu.Unlock()
}

func TestEnvSubst(t *testing.T) {
	resetEnvCol(t)
	resetHistory(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		io.WriteString(w, `{"path":"`+r.URL.Path+`","body":"`+string(body)+`"}`)
	}))
	defer srv.Close()

	svc := &HTTPService{}
	err := svc.SaveEnvState(EnvState{
		Active: "dev",
		Envs: []Environment{{
			Name: "dev",
			Vars: []KV{
				{Key: "base", Value: srv.URL, Enabled: true},
				{Key: "off", Value: "SHOULD-NOT-APPEAR", Enabled: false},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := svc.Send(&HTTPRequest{Method: "POST", URL: "{{base}}/echo", Body: "{{off}}"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Body, `"path":"/echo"`) {
		t.Fatalf("url not substituted: %s", resp.Body)
	}
	if !strings.Contains(resp.Body, `"body":"{{off}}"`) {
		t.Fatalf("disabled var should stay template: %s", resp.Body)
	}

	entries, _ := svc.GetHistory()
	if len(entries) != 1 || entries[0].Request.URL != "{{base}}/echo" {
		t.Fatalf("history should keep raw template: %+v", entries)
	}
}

func TestCollectionsRoundTrip(t *testing.T) {
	resetEnvCol(t)
	svc := &HTTPService{}

	nodes := []*CollectionNode{{
		ID: "c1", Name: "demo", Type: "collection",
		Children: []*CollectionNode{{
			ID: "r1", Name: "get x", Type: "request",
			Request: &HTTPRequest{Method: "GET", URL: "http://x/api"},
		}},
	}}
	if err := svc.SaveCollections(nodes); err != nil {
		t.Fatal(err)
	}

	// 模拟重启后重新加载
	colOnce = sync.Once{}
	colMu.Lock()
	colCache = nil
	colMu.Unlock()

	got, err := svc.GetCollections()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "demo" || len(got[0].Children) != 1 {
		t.Fatalf("reload wrong: %+v", got)
	}
	if got[0].Children[0].Request.URL != "http://x/api" {
		t.Fatalf("request lost: %+v", got[0].Children[0].Request)
	}
}
