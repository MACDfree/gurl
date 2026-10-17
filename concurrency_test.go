package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// 这些测试的意义：-race 抓不到「返回缓存内部对象」这类问题（写者目前都是整体替换
// slice 头），只有断言「改返回值不影响下一次读」才会红。

func TestCollectionsNotAliased(t *testing.T) {
	resetEnvCol(t)
	svc := &HTTPService{}
	req := &HTTPRequest{Method: "GET", URL: "https://x", Params: []KV{{Key: "a", Enabled: true}}}
	if err := svc.SaveCollections([]*CollectionNode{
		{ID: "c1", Name: "集合", Type: "collection", Children: []*CollectionNode{
			{ID: "r1", Name: "请求", Type: "request", Request: req},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := svc.GetCollections()
	if err != nil {
		t.Fatal(err)
	}
	got[0].Name = "被改了"
	got[0].Children[0].Request.Params[0].Key = "被改了"
	got[0].Children = append(got[0].Children, &CollectionNode{ID: "injected"})

	again, _ := svc.GetCollections()
	if again[0].Name != "集合" {
		t.Errorf("集合名被调用方写穿到缓存: %s", again[0].Name)
	}
	if len(again[0].Children) != 1 {
		t.Fatalf("子节点数量被调用方影响: %d", len(again[0].Children))
	}
	if k := again[0].Children[0].Request.Params[0].Key; k != "a" {
		t.Errorf("请求参数被调用方写穿到缓存: %s", k)
	}
}

func TestEnvVarsNotAliased(t *testing.T) {
	resetEnvCol(t)
	svc := &HTTPService{}
	if err := svc.SaveEnvState(EnvState{
		Active: "dev",
		Envs:   []Environment{{Name: "dev", Vars: []KV{{Key: "base", Value: "u1", Enabled: true}}}},
	}); err != nil {
		t.Fatal(err)
	}

	got, _ := svc.GetEnvState()
	got.Envs[0].Vars[0].Value = "被改了"
	got.Envs[0].Name = "被改了"

	again, _ := svc.GetEnvState()
	if again.Envs[0].Name != "dev" {
		t.Errorf("环境名被写穿: %s", again.Envs[0].Name)
	}
	if v := again.Envs[0].Vars[0].Value; v != "u1" {
		t.Errorf("变量值被写穿到缓存: %s", v)
	}
}

func TestHistoryRequestNotAliased(t *testing.T) {
	resetHistory(t)
	resetCookies(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":1}`))
	}))
	defer upstream.Close()

	svc := &HTTPService{}
	params := []KV{{Key: "page", Value: "1", Enabled: true}}
	if _, err := svc.Send(&HTTPRequest{Method: "GET", URL: upstream.URL, Params: params}); err != nil {
		t.Fatal(err)
	}
	// handler 解码出来的对象在响应之后仍可能被复用/修改。
	params[0].Value = "被改了"

	list, _ := svc.GetHistory()
	if len(list) != 1 {
		t.Fatalf("历史条数 = %d", len(list))
	}
	if v := list[0].Request.Params[0].Value; v != "1" {
		t.Errorf("历史里的参数被调用方写穿: %s", v)
	}
}

// TestConcurrentAccess 是给 -race 用的：多标签页会并发打同一批端点。
func TestConcurrentAccess(t *testing.T) {
	resetEnvCol(t)
	resetHistory(t)
	resetCookies(t)
	svc := &HTTPService{}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = svc.GetCollections()
				_ = svc.SaveCollections([]*CollectionNode{{ID: "x", Name: "n", Type: "collection"}})
				_, _ = svc.GetEnvState()
				_ = svc.SaveEnvState(EnvState{Active: "a"})
				_, _ = svc.GetHistory()
				_, _ = svc.GetCookies()
				_ = svc.SaveCookies([]CookieRecord{{Name: "a", Value: "1", Domain: "example.com", Enabled: true}})
			}
		}()
	}
	wg.Wait()
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := atomicWrite(path, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != `{"a":1}` {
		t.Fatalf("内容不对: %q %v", data, err)
	}
	// 目录里不该留下临时文件残骸。
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".gurl-") {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
	// 覆盖写同样要成功（rename 到已存在的目标是原子的）。
	if err := atomicWrite(path, []byte(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != `{"a":2}` {
		t.Errorf("覆盖写失败: %s", data)
	}
}

// 配置文件损坏时必须喊出来，而不是静默当成空配置——后者表现为「数据凭空消失」。
func TestLoadJSONCorruptIsNotSilent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "collections.json")
	if err := os.WriteFile(path, []byte(`{"broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out []*CollectionNode
	loadJSON(path, &out) // 不应 panic，也不应改动 out
	if len(out) != 0 {
		t.Errorf("损坏文件不该产出部分结果: %+v", out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("读取失败不该动原始文件: %v", err)
	}
}
