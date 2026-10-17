package main

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
)

// Auth 是「认证」页签里那份预设。存在的理由不是省事，而是让凭据和请求一起存进集合：
// 手抄 Authorization 头迟早会漏掉一个 Bearer，或者把过期 token 留在三个请求里。
//
// 值里可以写 {{token}}：替换在 applyEnv 之后、发送之前，跟其它字段同一时机。
type Auth struct {
	Type  string `json:"type"` // none | basic | bearer | apikey
	User  string `json:"user,omitempty"`
	Pass  string `json:"pass,omitempty"`
	Token string `json:"token,omitempty"`
	// apikey 用：Key 是头名或查询参数名，Value 是值。
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
	// In：header（默认）| query。query 类 key 只能加在 URL 上，别偷偷改成头。
	In string `json:"in,omitempty"`
}

func (a *Auth) active() bool {
	return a != nil && a.Type != "" && a.Type != "none"
}

// applyQueryAuth 在 URL 编码之前调用：query 类的 key 要并进参数里。
func applyQueryAuth(a *Auth, q url.Values) {
	if !a.active() || a.Type != "apikey" || !strings.EqualFold(a.In, "query") || a.Key == "" {
		return
	}
	q.Add(a.Key, a.Value)
}

// applyHeaderAuth 在手写头之后调用，且**不覆盖**用户已经写好的同名头——
// 和 Cookie 罐那条规则一致：显式写的东西不该被预设悄悄盖掉。
func applyHeaderAuth(a *Auth, h http.Header) {
	if !a.active() {
		return
	}
	// 只有 apikey 有「放查询参数」这回事。别拿 in 去挡 basic/bearer：
	// 界面上换过类型可能留下上一份的 in 字段，那样会静默不发认证。
	if a.Type == "apikey" && strings.EqualFold(a.In, "query") {
		return
	}
	switch a.Type {
	case "basic":
		setIfAbsent(h, "Authorization", "Basic "+basicAuth(a.User, a.Pass))
	case "bearer":
		if strings.TrimSpace(a.Token) != "" {
			setIfAbsent(h, "Authorization", "Bearer "+a.Token)
		}
	case "apikey":
		if a.Key != "" {
			setIfAbsent(h, a.Key, a.Value)
		}
	}
}

// basicAuth 按 UTF-8 编 base64（浏览器端 fetch 的 btoa 只吃 latin1，这里不受它限制）。
func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

func setIfAbsent(h http.Header, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	if h.Get(key) != "" {
		return // 已经有了（多半是用户手写的），不越权
	}
	h.Set(key, value)
}
