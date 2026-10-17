package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// SSEEvent 是一条 Server-Sent Event。多个 data: 行已按规范用 \n 拼成一个 Data。
type SSEEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data string `json:"data"`
}

type streamMeta struct {
	Status     int    `json:"status"`
	StatusText string `json:"statusText"`
	Headers    []KV   `json:"headers"`
}

// streamFrame 是 /api/stream 的一行 NDJSON。
//
// 为什么不把上游字节原样透传：那等于把「按空行分帧」推给前端，而 chunk 边界和 SSE
// 边界毫无关系，前端只会拿到更多半截帧。分帧在 Go 里做一次、测一次，前端只管按行拆 JSON。
type streamFrame struct {
	Type       string        `json:"type"` // response | meta | event | end | error
	Response   *HTTPResponse `json:"response,omitempty"`
	Meta       *streamMeta   `json:"meta,omitempty"`
	Event      *SSEEvent     `json:"event,omitempty"`
	Error      string        `json:"error,omitempty"`
	DurationMS int64         `json:"durationMs,omitempty"`
	Size       int64         `json:"size,omitempty"`
	Truncated  bool          `json:"truncated,omitempty"`
	// Timings 在 meta 和 end 上各给一次：meta 时首字节已经拿到（流式里最想看的就是这个数），
	// end 时正文时间才是完整的。
	Timings *Timings `json:"timings,omitempty"`
}

func isEventStream(resp *http.Response) bool {
	return mediaType(resp) == "text/event-stream"
}

// sendStream 发请求并把结果逐帧交给 emit；返回的 HTTPResponse 只用于记历史。
//
// 只有上游 Content-Type 是 text/event-stream 才走 meta/event/end 逐帧推送，否则整条
// 走原来的全缓冲路径、回一帧 response。判定看响应而不是看用户勾了什么，所以不必先猜对
// 上游会不会流式；前端也因此只有一条代码路径。
//
// timeoutMs 在这里只约束「连上并拿到响应头」：SSE 连接本来就不结束，而 Client.Timeout
// 是从发起到读完正文一起掐，用它等于几十秒后自己把流撕掉。代价是正文阶段没有总时限，
// 只有 hardCap 那道闸和用户手动的「停止」。
func (h *HTTPService) sendStream(ctx context.Context, req *HTTPRequest, emit func(streamFrame) error) (*HTTPResponse, error) {
	httpReq, timeout, opts, err := buildRequest(req)
	if err != nil {
		return nil, err
	}
	// 流式不设 Client.Timeout：那个期限由下面的 AfterFunc 只管「拿到响应头」这一段
	client, err := clientFor(opts, 0, jarFor(httpReq))
	if err != nil {
		return nil, err
	}
	ph := newPhaseTimes()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // 两条分支都在本函数内读满正文，返回时已经没有可取消的东西
	var headerTimedOut atomic.Bool
	// 用 AfterFunc 而不是 context.WithTimeout：拿不到响应头之前它得响，之后要能整个撤掉，
	// 而 WithTimeout 的期限撤不掉，会把正在读的正文一起掐了。
	timer := time.AfterFunc(timeout, func() {
		headerTimedOut.Store(true)
		cancel()
	})

	start := time.Now()
	resp, err := client.Do(httpReq.WithContext(ph.attach(ctx)))
	timer.Stop()
	if err != nil {
		if headerTimedOut.Load() {
			return nil, fmt.Errorf("等待响应头超时（%dms）", timeout.Milliseconds())
		}
		return nil, err
	}
	defer resp.Body.Close()

	if !isEventStream(resp) {
		full, err := bufferedResponse(resp, start)
		if full == nil {
			return nil, err
		}
		full.Timings = ph.collect(time.Since(start), resp)
		full.Extracted = applyExtract(req, full)
		return full, emit(streamFrame{Type: "response", Response: full})
	}

	out := &HTTPResponse{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Headers:    flatHeaders(resp.Header),
		IsText:     true,
	}
	if err := emit(streamFrame{Type: "meta", Meta: &streamMeta{
		Status: out.Status, StatusText: out.StatusText, Headers: out.Headers,
	}, Timings: ph.collect(time.Since(start), resp)}); err != nil {
		return out, err
	}
	dec, err := decodedBody(resp)
	if err != nil {
		return out, err
	}
	size, truncated, err := readSSE(dec, emit)
	out.Size = size
	out.Truncated = truncated
	out.DurationMS = time.Since(start).Milliseconds()
	out.Timings = ph.collect(time.Since(start), resp)
	// 事件流没有「完整正文」，所以这里只有表头类规则能命中；正文规则拿不到值，
	// 前端按 extracted 里缺谁去提示，不另开错误通道。
	out.Extracted = applyExtract(req, out)
	if err != nil {
		return out, err
	}
	return out, emit(streamFrame{Type: "end", DurationMS: out.DurationMS, Size: out.Size,
		Truncated: out.Truncated, Timings: out.Timings})
}

// countReader 数出真正从网络读到的字节，让流式路径的 size 和全缓冲路径同口径。
type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// readSSE 分帧并每凑齐一帧立刻 emit 一次——攒批转发等于把流式改回「等一批再给」。
// 返回读到的总字节、是否触到 hardCap、错误。
func readSSE(r io.Reader, emit func(streamFrame) error) (int64, bool, error) {
	cnt := &countReader{r: io.LimitReader(r, hardCap+1)}
	done := func(err error) (int64, bool, error) {
		if cnt.n > hardCap {
			return hardCap, true, err
		}
		return cnt.n, false, err
	}
	lines := &sseLines{br: bufio.NewReaderSize(cnt, 16<<10)}
	p := sseParser{emit: emit}
	for {
		line, err := lines.next()
		if perr := p.line(line); perr != nil {
			return done(perr)
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return done(p.flush())
		}
		return done(readInterrupted(err, int(cnt.n)))
	}
}

// sseLines 按 SSE 认可的三种换行（\r\n、\r、\n）切行。
//
// 见到 lone \r 不能用 Peek 判断后面是不是 \n——那会阻塞等上游再发一个字节，把已经
// 读到的事件卡住。改成记一位「下一行开头的 \n 属于上次换行」，读到时丢掉。
type sseLines struct {
	br     *bufio.Reader
	skipLF bool
}

func (s *sseLines) next() (string, error) {
	var line []byte
	for {
		b, err := s.br.ReadByte()
		if err != nil {
			return string(line), err
		}
		switch b {
		case '\n':
			if s.skipLF && len(line) == 0 {
				s.skipLF = false
				continue // 这是 \r\n 的第二个字节，不是新的一行
			}
			s.skipLF = false
			return string(line), nil
		case '\r':
			s.skipLF = true
			return string(line), nil
		default:
			s.skipLF = false
			line = append(line, b)
		}
	}
}

// sseParser 就是规范里的那套缓冲：data 逐行累加、空行派发、注释丢弃。
type sseParser struct {
	emit      func(streamFrame) error
	id        string
	eventType string
	data      []byte
}

func (p *sseParser) line(line string) error {
	if line == "" {
		return p.dispatch()
	}
	if line[0] == ':' {
		return nil // 注释，上游常拿来当 keepalive
	}
	field, value := line, ""
	if i := strings.IndexByte(line, ':'); i >= 0 {
		field, value = line[:i], strings.TrimPrefix(line[i+1:], " ")
	}
	switch field {
	case "event":
		p.eventType = value
	case "data":
		p.data = append(p.data, value...)
		p.data = append(p.data, '\n')
	case "id":
		if !strings.ContainsRune(value, 0) {
			p.id = value
		}
	case "retry":
		// gurl 不自动重连，所以重连间隔收到也没地方用。
	}
	return nil
}

func (p *sseParser) dispatch() error {
	if len(p.data) == 0 {
		p.eventType = "" // 规范：没有 data 就不派发，但事件名缓冲照样清掉
		return nil
	}
	ev := SSEEvent{ID: p.id, Type: p.eventType, Data: strings.TrimSuffix(string(p.data), "\n")}
	if ev.Type == "" {
		ev.Type = "message"
	}
	p.data = p.data[:0]
	p.eventType = ""
	return p.emit(streamFrame{Type: "event", Event: &ev})
}

// flush 处理「流结束了但最后那个空行没写」的上游。规范是丢弃未派发的缓冲，可对一个
// 调试工具来说，「最后一条回答凭空没了」比多显示一条糟得多。
func (p *sseParser) flush() error {
	return p.dispatch()
}
