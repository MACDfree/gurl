package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ncruces/zenity"
)

const maxBodySize = 2 << 20 // 2 MiB

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
	DurationMS int64  `json:"durationMs"`
	Size       int64  `json:"size"`
}

type HTTPService struct{}

func readDecodedBody(resp *http.Response) ([]byte, error) {
	var reader io.Reader = resp.Body
	switch strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))) {
	case "gzip":
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip 解压失败: %w", err)
		}
		defer zr.Close()
		reader = zr
	case "deflate":
		fr := flate.NewReader(resp.Body)
		defer fr.Close()
		reader = fr
	}
	return io.ReadAll(io.LimitReader(reader, maxBodySize+1))
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

	data, err := readDecodedBody(resp)
	if err != nil {
		return nil, err
	}
	duration := time.Since(start)

	truncated := len(data) > maxBodySize
	if truncated {
		data = data[:maxBodySize]
	}

	media := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	isText := strings.HasPrefix(media, "text/") ||
		strings.Contains(media, "json") ||
		strings.Contains(media, "xml") ||
		strings.Contains(media, "javascript") ||
		strings.Contains(media, "form") ||
		utf8.Valid(data)

	headers := make([]KV, 0, len(resp.Header))
	for k, vs := range resp.Header {
		headers = append(headers, KV{Key: k, Value: strings.Join(vs, ", "), Enabled: true})
	}

	return &HTTPResponse{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Headers:    headers,
		Body:       string(data),
		IsText:     isText,
		Truncated:  truncated,
		DurationMS: duration.Milliseconds(),
		Size:       int64(len(data)),
	}, nil
}
