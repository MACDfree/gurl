package main

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

// Timings 是一次请求各阶段花掉的时间（毫秒）。字段为 0 表示该阶段没发生：
// 复用连接就没有 DNS/连接/TLS，直连 IP 不做解析，没跳转就没有 hops。
// 真的发生了但不足 1ms 的，按 1ms 报——「0 = 没做」这个约定不能被四舍五入打破。
type Timings struct {
	DNSMs     int64 `json:"dnsMs,omitempty"`
	ConnectMs int64 `json:"connectMs,omitempty"`
	TLSMs     int64 `json:"tlsMs,omitempty"`
	// TTFBMs：发出请求 → 收到响应头。跳转链上的每一跳都算在里面，这正是用户感知的等待。
	TTFBMs int64 `json:"ttfbMs,omitempty"`
	// BodyMs：响应头 → 正文读完。流式请求这里是「读到当前为止」的累计。
	BodyMs int64 `json:"bodyMs,omitempty"`
	Reused bool  `json:"reused,omitempty"`
	Hops   int   `json:"hops,omitempty"` // 跟了几次 30x
}

// phaseTimes 收 httptrace 的事件。回调跑在 transport 自己的 goroutine 里，
// 与读正文的主 goroutine 并发，所以全部字段由 mu 守（-race 下这是必须的）。
type phaseTimes struct {
	mu        sync.Mutex
	t0        time.Time
	dnsStart  time.Time
	connStart time.Time
	tlsStart  time.Time
	dnsMs     int64
	connMs    int64
	tlsMs     int64
	ttfbMs    int64
	reused    bool
}

// atLeast1ms：阶段发生过就至少记 1ms，别让「0.3ms」和「没发生」撞成同一个值。
func atLeast1ms(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	if ms := d.Milliseconds(); ms > 0 {
		return ms
	}
	return 1
}

// clientTrace 里各阶段按累加处理：30x 跳到另一个域名时 DNS/连接/TLS 会各做一遍，
// 加起来才是「这条请求总共花在哪」，只报最后一跳会偏小。
func (p *phaseTimes) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			p.mu.Lock()
			p.dnsStart = time.Now()
			p.mu.Unlock()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			p.mu.Lock()
			if !p.dnsStart.IsZero() {
				p.dnsMs += atLeast1ms(time.Since(p.dnsStart))
				p.dnsStart = time.Time{}
			}
			p.mu.Unlock()
		},
		ConnectStart: func(string, string) {
			p.mu.Lock()
			p.connStart = time.Now()
			p.mu.Unlock()
		},
		ConnectDone: func(string, string, error) {
			p.mu.Lock()
			if !p.connStart.IsZero() {
				p.connMs += atLeast1ms(time.Since(p.connStart))
				p.connStart = time.Time{}
			}
			p.mu.Unlock()
		},
		TLSHandshakeStart: func() {
			p.mu.Lock()
			p.tlsStart = time.Now()
			p.mu.Unlock()
		},
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			p.mu.Lock()
			if !p.tlsStart.IsZero() {
				p.tlsMs += atLeast1ms(time.Since(p.tlsStart))
				p.tlsStart = time.Time{}
			}
			p.mu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			p.mu.Lock()
			p.reused = p.reused || info.Reused
			p.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			p.mu.Lock()
			p.ttfbMs = atLeast1ms(time.Since(p.t0))
			p.mu.Unlock()
		},
	}
}

// collect 汇总成对外结构。total 是整条请求的耗时，正文时间由它减出来。
func (p *phaseTimes) collect(total time.Duration, resp *http.Response) *Timings {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := &Timings{
		DNSMs: p.dnsMs, ConnectMs: p.connMs, TLSMs: p.tlsMs, TTFBMs: p.ttfbMs, Reused: p.reused,
	}
	if ms := atLeast1ms(total); ms > t.TTFBMs {
		t.BodyMs = ms - t.TTFBMs
	}
	// 跳转链：resp.Request 是最后那个请求，它的 Response 是上一跳，一路回溯数跳数。
	if resp != nil {
		for cur := resp.Request; cur != nil && cur.Response != nil; cur = cur.Response.Request {
			t.Hops++
		}
	}
	return t
}

// newPhaseTimes 建一个采集器；t0 是各阶段的共同起点（≈ 发起请求的时刻）。
func newPhaseTimes() *phaseTimes {
	return &phaseTimes{t0: time.Now()}
}

// attach 把采集器挂到 ctx 上并返回新 context。
//
// 为什么不是「传个 *http.Request 进去」：流式路径会在发出去前最后一刻用
// httpReq.WithContext(cancelCtx) 换掉 context，trace 要是提前绑在请求上就会被这次替换
// 整个丢掉——各阶段静默归零，看起来像「本机请求太快」。所以谁做最终的 WithContext，
// 谁负责把 trace 放进去。
func (p *phaseTimes) attach(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, p.clientTrace())
}
