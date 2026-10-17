package main

import (
	"net/http"
	"testing"
)

func built(t *testing.T, req HTTPRequest) *http.Request {
	t.Helper()
	hr, _, err := buildRequest(&req)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	return hr
}

func TestAuthPresets(t *testing.T) {
	resetEnvCol(t)
	base := HTTPRequest{Method: "GET", URL: "https://api.dev/x", TimeoutMS: 5000}
	with := func(a *Auth, extra ...KV) HTTPRequest {
		r := base
		r.Auth = a
		r.Headers = extra
		return r
	}

	for _, c := range []struct {
		name string
		req  HTTPRequest
		hdr  string
		want string
	}{
		{"basic", with(&Auth{Type: "basic", User: "bob", Pass: "s3cret"}), "Authorization", "Basic Ym9iOnMzY3JldA=="},
		// 密码里有非 ASCII：base64(UTF-8)，与主流客户端一致
		{"basic 带中文", with(&Auth{Type: "basic", User: "用户", Pass: "密码"}), "Authorization", "Basic 55So5oi3OuWvhueggQ=="},
		{"bearer", with(&Auth{Type: "bearer", Token: "T-42"}), "Authorization", "Bearer T-42"},
		{"apikey 回头", with(&Auth{Type: "apikey", Key: "X-Api-Key", Value: "K9"}), "X-Api-Key", "K9"},
		{"none 什么都不加", with(&Auth{Type: "none"}), "Authorization", ""},
		{"bearer 但没填 token", with(&Auth{Type: "bearer"}), "Authorization", ""},
		// 界面上换过类型可能残留 in=query：只有 apikey 该被它挡住，bearer 照常发头
		{"bearer 带着残留 in=query", with(&Auth{Type: "bearer", Token: "T-1", In: "query"}), "Authorization", "Bearer T-1"},
		// 手写的同名头优先：预设不该越权盖掉用户显式写的东西
		{"手写 Authorization 压过预设", with(&Auth{Type: "bearer", Token: "FROM-AUTH"},
			KV{Key: "Authorization", Value: "Bearer HAND-WRITTEN", Enabled: true}), "Authorization", "Bearer HAND-WRITTEN"},
	} {
		if got := built(t, c.req).Header.Get(c.hdr); got != c.want {
			t.Errorf("%s: %s = %q，期望 %q", c.name, c.hdr, got, c.want)
		}
	}

	// apikey 放查询参数：加在 URL 上，不该冒出一个头
	hr := built(t, with(&Auth{Type: "apikey", Key: "access_token", Value: "K9", In: "query"}))
	if got := hr.URL.Query().Get("access_token"); got != "K9" {
		t.Errorf("query 类 key 没并进 URL：%q", hr.URL.String())
	}
	if got := hr.Header.Get("access_token"); got != "" {
		t.Errorf("query 类 key 不该变成头：%q", got)
	}
}

// auth 里的 {{var}} 该和其它字段一样被替换，且替换不能写回界面上那份模板。
func TestAuthEnvSubstitution(t *testing.T) {
	resetEnvCol(t)
	svc := &HTTPService{}
	if err := svc.SaveEnvState(EnvState{Active: "dev", Envs: []Environment{{
		Name: "dev", Vars: []KV{{Key: "token", Value: "T-ENV", Enabled: true}},
	}}}); err != nil {
		t.Fatal(err)
	}
	req := HTTPRequest{
		Method: "GET", URL: "https://api.dev/x", TimeoutMS: 5000,
		Auth: &Auth{Type: "bearer", Token: "{{token}}"},
	}
	if got := built(t, req).Header.Get("Authorization"); got != "Bearer T-ENV" {
		t.Errorf("auth 里的变量没替换：%q", got)
	}
	if req.Auth.Token != "{{token}}" {
		t.Errorf("替换写回了原对象，界面上的模板被改坏了：%q", req.Auth.Token)
	}
}

// cloneRequest 必须把 Auth 复制开：缓存里的请求和交给调用方的那份不能共享同一个指针，
// 否则两个标签页并发编辑会互相改到对方的凭据。
func TestCloneRequestDetachesAuth(t *testing.T) {
	original := &Auth{Type: "basic", User: "bob", Pass: "a"}
	clone := cloneRequest(HTTPRequest{Method: "GET", URL: "https://api.dev", Auth: original})
	clone.Auth.Pass = "b"
	if original.Pass != "a" {
		t.Error("克隆出来的 Auth 与原对象共享，改一个动两个")
	}
}
