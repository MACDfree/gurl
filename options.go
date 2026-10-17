package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Options 是传输层那几个开关。默认值一律「和以前一样」：字段缺席就是关，
// 旧的集合与历史文件因此不用迁移，行为也不会因为加了这几个字段而改变。
type Options struct {
	// NoFollow：不跟 30x，把重定向响应原样交给用户（就是拿来看 Location 的）。
	NoFollow bool `json:"noFollow,omitempty"`
	// MaxRedirects 为 0 表示沿用 stdlib 的 10 跳上限。
	MaxRedirects int `json:"maxRedirects,omitempty"`
	// InsecureTLS 跳过证书校验，只该给自签证书的本机 / 内网上游用。
	InsecureTLS bool `json:"insecureTLS,omitempty"`
	// Proxy 例：http://127.0.0.1:8888、socks5://127.0.0.1:1080，也允许只写主机:端口。
	Proxy string `json:"proxy,omitempty"`
}

func (o *Options) none() bool { return o == nil || *o == Options{} }

// clientFor 造一次请求要用的 Client。
//
// 没要特殊开关时**不碰 Transport**，让它落回共享的 http.DefaultTransport——连接池在那儿，
// 为每个请求新开一套等于每次都重做 TCP + TLS。要了跳过校验或代理，才换一个按组合缓存的
// 定制 transport：同一种 (insecure, proxy) 复用同一个池，不同组合互不干扰。
func clientFor(o *Options, timeout time.Duration, jar http.CookieJar) (*http.Client, error) {
	c := &http.Client{Timeout: timeout, Jar: jar}
	if o == nil {
		return c, nil
	}
	if o.NoFollow || o.MaxRedirects > 0 {
		noFollow, max := o.NoFollow, o.MaxRedirects
		c.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
			if noFollow {
				return http.ErrUseLastResponse
			}
			// via[0] 是原始请求，所以第 N 次跳转时 len(via) == N+1
			if max > 0 && len(via) > max {
				return fmt.Errorf("重定向次数超过设的 %d 次上限", max)
			}
			return nil
		}
	}
	if !o.InsecureTLS && strings.TrimSpace(o.Proxy) == "" {
		return c, nil
	}
	tr, err := transportFor(o.InsecureTLS, strings.TrimSpace(o.Proxy))
	if err != nil {
		return nil, err
	}
	c.Transport = tr
	return c, nil
}

var (
	trMu    sync.Mutex
	trCache = map[string]*http.Transport{}
)

// transportFor 按 (跳过校验, 代理) 组合缓存 transport。组合数天然很少（一个人调试时
// 通常就一两个代理），但上限还是给一个：超了就现造不缓存，最坏是少一个连接池，
// 而不是无界涨。
func transportFor(insecure bool, proxy string) (*http.Transport, error) {
	key := fmt.Sprintf("%t|%s", insecure, proxy)
	trMu.Lock()
	defer trMu.Unlock()
	if t, ok := trCache[key]; ok {
		return t, nil
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if proxy != "" {
		u, err := parseProxy(proxy)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(u)
	}
	if len(trCache) < 16 {
		trCache[key] = tr
	}
	return tr, nil
}

// parseProxy 宁可报错也不要悄悄忽略：代理写错却照常发出去，用户会以为流量走了抓包工具。
func parseProxy(raw string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("代理地址无法解析：%s", raw)
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("代理协议只认 http / https / socks5，收到 %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("代理地址缺少主机名：%s", raw)
	}
	return u, nil
}
