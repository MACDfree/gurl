package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ncruces/zenity"
)

const (
	previewCap = 5 << 20  // 内联进响应的字节上限，超出部分落盘缓存
	hardCap    = 64 << 20 // 从网络读满并缓存的上限
)

type KV struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
}

type FileKV struct {
	Key  string `json:"key"`
	Path string `json:"path"`
}

type HTTPRequest struct {
	Method    string   `json:"method"`
	URL       string   `json:"url"`
	Params    []KV     `json:"params"`
	Headers   []KV     `json:"headers"`
	BodyType  string   `json:"bodyType"` // none | json | raw | form | multipart；空按 raw 处理
	Body      string   `json:"body"`
	Form      []KV     `json:"form"`
	Files     []FileKV `json:"files"`
	BodyGzip  bool     `json:"bodyGzip"`
	TimeoutMS int      `json:"timeoutMs"`
	// Extract 是「从这次响应里取值存成运行时变量」的规则，见 extract.go。
	Extract []ExtractRule `json:"extract,omitempty"`
	// Auth 是认证预设；nil 或 type=none 表示没有。
	Auth *Auth `json:"auth,omitempty"`
	// Opts 是传输层开关（不跟跳转 / 跳数上限 / 跳过证书校验 / 代理），见 options.go。
	Opts *Options `json:"opts,omitempty"`
}

type HTTPResponse struct {
	Status     int    `json:"status"`
	StatusText string `json:"statusText"`
	Headers    []KV   `json:"headers"`
	Body       string `json:"body"`
	IsText     bool   `json:"isText"`
	Truncated  bool   `json:"truncated"`
	// BodyID 非空表示完整正文已落盘，可通过 /api/response/{id} 取回。
	BodyID     string `json:"bodyId,omitempty"`
	DurationMS int64  `json:"durationMs"`
	Size       int64  `json:"size"`
	// Timings 是各阶段耗时分解（DNS/连接/TLS/首字节/正文）。
	Timings *Timings `json:"timings,omitempty"`
	// Extracted 是本次响应按规则取到的运行时变量；没取到的名字不出现，
	// 前端拿规则和它一比就知道哪条没命中。
	Extracted map[string]string `json:"extracted,omitempty"`
}

type HTTPService struct{}

// cloneKVs / cloneRequest 是深拷贝辅助。
//
// 改成 HTTP 后端之后，多个浏览器标签页会并发读写同一份缓存；缓存里的对象交给调用方
// 之前必须与缓存完全脱钩，否则任何一处原地修改都会变成真数据竞争（而且 -race 今天
// 抓不到，因为写者目前都是整体替换 slice 头）。
func cloneKVs(in []KV) []KV {
	if in == nil {
		return nil
	}
	out := make([]KV, len(in))
	copy(out, in)
	return out
}

func cloneFileKVs(in []FileKV) []FileKV {
	if in == nil {
		return nil
	}
	out := make([]FileKV, len(in))
	copy(out, in)
	return out
}

func cloneRequest(in HTTPRequest) HTTPRequest {
	out := in
	out.Params = cloneKVs(in.Params)
	out.Headers = cloneKVs(in.Headers)
	out.Form = cloneKVs(in.Form)
	out.Files = cloneFileKVs(in.Files)
	if in.Extract != nil {
		out.Extract = append([]ExtractRule(nil), in.Extract...)
	}
	if in.Auth != nil {
		a := *in.Auth
		out.Auth = &a
	}
	if in.Opts != nil {
		o := *in.Opts
		out.Opts = &o
	}
	return out
}

func cloneHTTPRequest(in *HTTPRequest) *HTTPRequest {
	if in == nil {
		return nil
	}
	c := cloneRequest(*in)
	return &c
}

// decodedBody 返回解压后的 reader（不是全部字节）：调用方先只读 previewCap+1 走快路径，
// 超限再把剩余部分边读边拷进缓存文件。
func decodedBody(resp *http.Response) (io.Reader, error) {
	var reader io.Reader = resp.Body
	switch strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))) {
	case "gzip":
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip 解压失败: %w", err)
		}
		reader = zr
	case "deflate":
		reader = flate.NewReader(resp.Body)
	}
	return reader, nil
}

func buildBody(req *HTTPRequest) ([]byte, string, error) {
	switch req.BodyType {
	case "none":
		return nil, "", nil
	case "json":
		return []byte(req.Body), "application/json", nil
	case "form":
		v := url.Values{}
		for _, f := range req.Form {
			if f.Enabled && f.Key != "" {
				v.Add(f.Key, f.Value)
			}
		}
		return []byte(v.Encode()), "application/x-www-form-urlencoded", nil
	case "multipart":
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for _, f := range req.Form {
			if f.Enabled && f.Key != "" {
				if err := mw.WriteField(f.Key, f.Value); err != nil {
					return nil, "", err
				}
			}
		}
		for _, fl := range req.Files {
			if fl.Key == "" || fl.Path == "" {
				continue
			}
			part, err := mw.CreateFormFile(fl.Key, filepath.Base(fl.Path))
			if err != nil {
				return nil, "", err
			}
			src, err := os.Open(fl.Path)
			if err != nil {
				return nil, "", fmt.Errorf("打开文件 %s 失败: %w", fl.Path, err)
			}
			_, cpErr := io.Copy(part, src)
			src.Close()
			if cpErr != nil {
				return nil, "", fmt.Errorf("读取文件 %s 失败: %w", fl.Path, cpErr)
			}
		}
		ct := mw.FormDataContentType()
		if err := mw.Close(); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), ct, nil
	default: // "" 或 "raw"
		return []byte(req.Body), "", nil
	}
}

// PickFile 弹原生文件选择框，返回服务端绝对路径。
//
// 取消被映射成 ("", nil)：取消不是错误，HTTP 层因此能回 ok:true + data:""，
// 前端不必为它专门分支（Wails 版会抛一个未捕获的 rejection）。
func (h *HTTPService) PickFile() (string, error) {
	path, err := zenity.SelectFile(zenity.Title("选择上传文件"))
	if errors.Is(err, zenity.ErrCanceled) {
		return "", nil
	}
	return path, err
}

// mediaType 取 Content-Type 分号前的部分，调用方拿到的是已小写、已去空白的纯类型。
func mediaType(resp *http.Response) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
}

// flatHeaders 摊平响应表头。排序是必需的：不排的话同一请求两次会得到不同顺序的表头。
func flatHeaders(h http.Header) []KV {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	headers := make([]KV, 0, len(keys))
	for _, k := range keys {
		headers = append(headers, KV{Key: k, Value: strings.Join(h[k], ", "), Enabled: true})
	}
	return headers
}

// buildRequest 把界面上的请求变成上游请求：环境变量替换、query 合并、body 构造与
// gzip、表头。返回的 timeout 是这条请求的期限，全缓冲与流式各自决定它约束哪一段；
// 顺带回带替换过的传输层开关，调用方不必再自己走一遍 applyEnv。
func buildRequest(req *HTTPRequest) (*http.Request, time.Duration, *Options, error) {
	if req == nil {
		return nil, 0, nil, errors.New("请求为空")
	}
	if req.URL == "" {
		return nil, 0, nil, errors.New("URL 不能为空")
	}
	req = applyEnv(req)
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}

	fullURL, err := url.Parse(req.URL)
	if err != nil {
		return nil, 0, nil, err
	}
	if fullURL.Scheme == "" {
		fullURL.Scheme = "https"
	}
	q := fullURL.Query()
	for _, p := range req.Params {
		if p.Enabled && p.Key != "" {
			q.Add(p.Key, p.Value)
		}
	}
	applyQueryAuth(req.Auth, q)
	fullURL.RawQuery = q.Encode()

	bodyBytes, defaultCT, err := buildBody(req)
	if err != nil {
		return nil, 0, nil, err
	}
	if req.BodyGzip && len(bodyBytes) > 0 {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(bodyBytes); err != nil {
			return nil, 0, nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, 0, nil, err
		}
		bodyBytes = buf.Bytes()
	}

	var body io.Reader
	if len(bodyBytes) > 0 {
		body = bytes.NewReader(bodyBytes)
	}
	httpReq, err := http.NewRequest(method, fullURL.String(), body)
	if err != nil {
		return nil, 0, nil, err
	}
	if defaultCT != "" {
		httpReq.Header.Set("Content-Type", defaultCT)
	}
	for _, hd := range req.Headers {
		if hd.Enabled && hd.Key != "" {
			httpReq.Header.Set(hd.Key, hd.Value)
		}
	}
	// 预设排在手写之后：同名头不越权（见 auth.go）
	applyHeaderAuth(req.Auth, httpReq.Header)
	if req.BodyGzip && len(bodyBytes) > 0 {
		httpReq.Header.Set("Content-Encoding", "gzip")
	}

	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return httpReq, timeout, req.Opts, nil
}

func (h *HTTPService) send(req *HTTPRequest) (*HTTPResponse, error) {
	httpReq, timeout, opts, err := buildRequest(req)
	if err != nil {
		return nil, err
	}
	client, err := clientFor(opts, timeout, jarFor(httpReq))
	if err != nil {
		return nil, err
	}
	ph := newPhaseTimes()
	httpReq = httpReq.WithContext(ph.attach(httpReq.Context()))

	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	out, err := bufferedResponse(resp, start)
	if out != nil {
		out.Timings = ph.collect(time.Since(start), resp)
		out.Extracted = applyExtract(req, out)
	}
	return out, err
}

// bufferedResponse 收满正文并判定文本/二进制。流式端点遇到非 SSE 上游时也走这里，
// 所以两条路径给出的 HTTPResponse 形状完全一致。
func bufferedResponse(resp *http.Response, start time.Time) (*HTTPResponse, error) {
	reader, err := decodedBody(resp)
	if err != nil {
		return nil, err
	}
	duration := time.Since(start)

	// 类型在读正文之前就知道（响应头已经到手），媒体响应要靠它决定要不要落盘。
	media := mediaType(resp)
	head, size, truncated, bodyID, err := captureBody(reader, resp.Request.URL.String(), media)
	if err != nil {
		return nil, err
	}

	probe := head
	// 截断点很可能落在多字节字符中间，直接 utf8.Valid(head) 会把纯文本误判成二进制。
	// 从尾部回退到最后一个完整字符的边界（最多 UTFMax 次，每次 O(1)）。
	if truncated {
		for i := 0; i < utf8.UTFMax && len(probe) > 0; i++ {
			if r, n := utf8.DecodeLastRune(probe); r != utf8.RuneError || n != 1 {
				break
			}
			probe = probe[:len(probe)-1]
		}
	}
	isText := strings.HasPrefix(media, "text/") ||
		strings.Contains(media, "json") ||
		strings.Contains(media, "xml") ||
		strings.Contains(media, "javascript") ||
		strings.Contains(media, "form") ||
		utf8.Valid(probe)

	return &HTTPResponse{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Headers:    flatHeaders(resp.Header),
		Body:       string(head),
		IsText:     isText,
		Truncated:  truncated,
		BodyID:     bodyID,
		DurationMS: duration.Milliseconds(),
		Size:       size,
	}, nil
}

// captureBody 先读 previewCap+1 走快路径；确实超限时才把剩余部分边读边拷进落盘缓存。
// 返回的 size 是「从网络读到的总字节数」，不是预览长度。
//
// 读中断必须作为错误返回：原来把 io.ReadAll 的 error 丢了，于是连接传到一半被重置
// 也长得像一个成功的完整响应（truncated 还是 false），对调试工具是最糟的假象。
func captureBody(reader io.Reader, rawURL, media string) (head []byte, size int64, truncated bool, bodyID string, err error) {
	raw, rerr := io.ReadAll(io.LimitReader(reader, previewCap+1))
	if rerr != nil {
		return nil, int64(len(raw)), false, "", readInterrupted(rerr, len(raw))
	}
	if len(raw) <= previewCap {
		if !inlineableMedia(media) {
			return raw, int64(len(raw)), false, "", nil
		}
		// 媒体响应即使不大也整个落盘：预览要的是一个能直接喂给 <img> 的 URL，
		// 而不是把几 MB 字节塞进 JSON。正文本身仍然进不了 DOM（非文本），
		// 所以这条只是多写一个临时文件，换来「能看」和「能下载」同一个来源。
		return raw, int64(len(raw)), false, cacheWhole(raw, rawURL, media), nil
	}
	// raw 里多读的那 1 个字节也是响应的一部分，计数时不能丢。
	size = int64(len(raw))
	cache := bodyCacheRef()
	id, f, cerr := cache.Create(downloadName(rawURL), media)
	if cerr != nil {
		log.Printf("超限响应无法落盘，只保留预览: %v", cerr)
		return raw[:previewCap], size, true, "", nil
	}
	// 缓存文件必须装完整正文：预览读掉的那部分已经在内存里，得先补写进去，
	// 否则下载到的只是一截尾巴。
	if _, err := f.Write(raw); err != nil {
		f.Close()
		cache.Discard(id)
		return raw[:previewCap], size, true, "", nil
	}
	n, rerr := io.Copy(f, io.LimitReader(reader, hardCap-size))
	size += n
	if rerr != nil {
		f.Close()
		cache.Discard(id)
		return nil, size, true, "", readInterrupted(rerr, int(size))
	}
	if size >= hardCap {
		f.Close()
		cache.Discard(id)
		return raw[:previewCap], size, true, "", nil
	}
	if cerr := f.Close(); cerr != nil {
		cache.Discard(id)
		return raw[:previewCap], size, true, "", nil
	}
	return raw[:previewCap], size, true, id, nil
}

// cacheWhole 把一份完整正文原样存进缓存，返回 id；存不下就返回空串（预览自然降级成没有）。
func cacheWhole(raw []byte, rawURL, media string) string {
	cache := bodyCacheRef()
	id, f, err := cache.Create(downloadName(rawURL), media)
	if err != nil {
		log.Printf("响应无法落盘，无法预览: %v", err)
		return ""
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		cache.Discard(id)
		return ""
	}
	if err := f.Close(); err != nil {
		cache.Discard(id)
		return ""
	}
	return id
}

func readInterrupted(err error, got int) error {
	return fmt.Errorf("读取响应体中断（已收到 %d 字节）: %w", got, err)
}
