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

func (h *HTTPService) send(req *HTTPRequest) (*HTTPResponse, error) {
	if req == nil {
		return nil, errors.New("请求为空")
	}
	if req.URL == "" {
		return nil, errors.New("URL 不能为空")
	}
	req = applyEnv(req)
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}

	fullURL, err := url.Parse(req.URL)
	if err != nil {
		return nil, err
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
	fullURL.RawQuery = q.Encode()

	bodyBytes, defaultCT, err := buildBody(req)
	if err != nil {
		return nil, err
	}
	if req.BodyGzip && len(bodyBytes) > 0 {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(bodyBytes); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		bodyBytes = buf.Bytes()
	}

	var body io.Reader
	if len(bodyBytes) > 0 {
		body = bytes.NewReader(bodyBytes)
	}
	httpReq, err := http.NewRequest(method, fullURL.String(), body)
	if err != nil {
		return nil, err
	}
	if defaultCT != "" {
		httpReq.Header.Set("Content-Type", defaultCT)
	}
	for _, hd := range req.Headers {
		if hd.Enabled && hd.Key != "" {
			httpReq.Header.Set(hd.Key, hd.Value)
		}
	}
	if req.BodyGzip && len(bodyBytes) > 0 {
		httpReq.Header.Set("Content-Encoding", "gzip")
	}

	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout}

	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	reader, err := decodedBody(resp)
	if err != nil {
		return nil, err
	}
	duration := time.Since(start)

	head, size, truncated, bodyID := captureBody(reader, req.URL)

	media := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
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

	keys := make([]string, 0, len(resp.Header))
	for k := range resp.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 不排序的话同一请求两次会得到不同顺序的表头
	headers := make([]KV, 0, len(keys))
	for _, k := range keys {
		headers = append(headers, KV{Key: k, Value: strings.Join(resp.Header[k], ", "), Enabled: true})
	}

	return &HTTPResponse{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Headers:    headers,
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
func captureBody(reader io.Reader, rawURL string) (head []byte, size int64, truncated bool, bodyID string) {
	raw, err := io.ReadAll(io.LimitReader(reader, previewCap+1))
	if err != nil {
		return raw, int64(len(raw)), false, ""
	}
	if len(raw) <= previewCap {
		return raw, int64(len(raw)), false, ""
	}
	// raw 里多读的那 1 个字节也是响应的一部分，计数时不能丢。
	size = int64(len(raw))
	cache := bodyCacheRef()
	id, f, cerr := cache.Create(downloadName(rawURL))
	if cerr != nil {
		log.Printf("超限响应无法落盘，只保留预览: %v", cerr)
		return raw[:previewCap], size, true, ""
	}
	// 缓存文件必须装完整正文：预览读掉的那部分已经在内存里，得先补写进去，
	// 否则下载到的只是一截尾巴。
	if _, err := f.Write(raw); err != nil {
		f.Close()
		cache.Discard(id)
		return raw[:previewCap], size, true, ""
	}
	n, _ := io.Copy(f, io.LimitReader(reader, hardCap-size))
	size += n
	if size >= hardCap {
		f.Close()
		cache.Discard(id)
		return raw[:previewCap], size, true, ""
	}
	if cerr := f.Close(); cerr != nil {
		cache.Discard(id)
		return raw[:previewCap], size, true, ""
	}
	return raw[:previewCap], size, true, id
}
