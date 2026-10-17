package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestLookupPath(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(`{"data":{"token":"abc","n":42,"ok":true,"tags":["x","y"],"obj":{"k":"v"},"nul":null},"items":[{"id":7}]}`), &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		want string
		ok   bool
	}{
		{"data.token", "abc", true},
		{"$.data.token", "abc", true}, // 容忍 jq 风格的 $ 前缀
		{"data.n", "42", true},        // 数字不能变成 42.000000
		{"data.ok", "true", true},
		{"data.tags[1]", "y", true},
		{"items[0].id", "7", true},
		{"data.obj", `{"k":"v"}`, true}, // 整段结构也能取，紧凑 JSON
		{"data.nul", "", true},
		{"data.missing", "", false},
		{"data.token.deeper", "", false}, // 标量下面没有子节点
		{"items[5].id", "", false},
	} {
		got, ok := lookupPath(doc, c.path)
		if ok != c.ok || got != c.want {
			t.Errorf("lookupPath(%q) = (%q, %v)，期望 (%q, %v)", c.path, got, ok, c.want, c.ok)
		}
	}
}

// 提取 → 运行时变量 → 下一个请求的 {{token}}，整条链；顺带验证它盖过同名环境变量、
// 以及「清空」确实把内存里那份抹掉。
func TestExtractChainsIntoNextRequest(t *testing.T) {
	resetHistory(t)
	resetEnvCol(t)
	resetCookies(t)
	replaceRuntime(nil)
	defer replaceRuntime(nil)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Header().Set("X-Via", "edge-7")
			io.WriteString(w, `{"data":{"token":"T-9527"}}`)
		case "/me":
			io.WriteString(w, `{"auth":"`+r.Header.Get("Authorization")+`"}`)
		}
	}))
	defer upstream.Close()

	svc := &HTTPService{}
	first, err := svc.send(&HTTPRequest{
		Method: "GET", URL: upstream.URL + "/login", TimeoutMS: 5000,
		Extract: []ExtractRule{
			{Name: "token", Path: "data.token"},
			{Name: "via", Source: "header", Path: "X-Via"},
			{Name: "missing", Path: "data.nope"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Extracted["token"]; got != "T-9527" {
		t.Errorf("extracted[token] = %q", got)
	}
	if got := first.Extracted["via"]; got != "edge-7" {
		t.Errorf("表头规则没命中：%q", got)
	}
	if _, bad := first.Extracted["missing"]; bad {
		t.Error("取不到的规则不该出现在 extracted 里")
	}

	second, err := svc.send(&HTTPRequest{
		Method: "GET", URL: upstream.URL + "/me", TimeoutMS: 5000,
		Headers: []KV{{Key: "Authorization", Value: "Bearer {{token}}", Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"auth":"Bearer T-9527"}`; second.Body != want {
		t.Errorf("第二个请求没拿到提取值：got %s want %s", second.Body, want)
	}

	// 同名环境变量应当被运行时变量盖住（提取的意义就是「这次跑出来的值」）
	if err := svc.SaveEnvState(EnvState{Active: "dev", Envs: []Environment{{
		Name: "dev", Vars: []KV{{Key: "token", Value: "FROM-ENV", Enabled: true}},
	}}}); err != nil {
		t.Fatal(err)
	}
	state, err := svc.GetEnvState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Runtime == nil || len(*state.Runtime) == 0 {
		t.Error("GetEnvState 该把运行时变量带出来给界面显示")
	}
	third, err := svc.send(&HTTPRequest{
		Method: "GET", URL: upstream.URL + "/me", TimeoutMS: 5000,
		Headers: []KV{{Key: "Authorization", Value: "Bearer {{token}}", Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"auth":"Bearer T-9527"}`; third.Body != want {
		t.Errorf("运行时变量没盖过环境变量：%s", third.Body)
	}

	// 只 PUT 环境（不提 runtime）不该动运行时变量——界面每次改环境都是这么发的。
	envs := []Environment{{Name: "dev", Vars: []KV{{Key: "token", Value: "FROM-ENV", Enabled: true}}}}
	if err := svc.SaveEnvState(EnvState{Active: "dev", Envs: envs}); err != nil {
		t.Fatal(err)
	}
	kept, err := svc.send(&HTTPRequest{
		Method: "GET", URL: upstream.URL + "/me", TimeoutMS: 5000,
		Headers: []KV{{Key: "Authorization", Value: "Bearer {{token}}", Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"auth":"Bearer T-9527"}`; kept.Body != want {
		t.Errorf("只改环境不该清掉运行时变量，实际 %s", kept.Body)
	}

	// 显式清空：runtime 给空数组。env.json 里也绝不能混进 runtime。
	empty := []KV{}
	if err := svc.SaveEnvState(EnvState{Active: "dev", Envs: envs, Runtime: &empty}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("env.json 不是合法 JSON: %v", err)
	}
	if _, ok := saved["runtime"]; ok {
		t.Error("运行时变量被写进 env.json 了——它不该落盘")
	}
	fourth, err := svc.send(&HTTPRequest{
		Method: "GET", URL: upstream.URL + "/me", TimeoutMS: 5000,
		Headers: []KV{{Key: "Authorization", Value: "Bearer {{token}}", Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"auth":"Bearer FROM-ENV"}`; fourth.Body != want {
		t.Errorf("清空后该回落到环境变量，实际 %s", fourth.Body)
	}
}
