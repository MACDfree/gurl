package core

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SendRequest 发送HTTP请求
func SendRequest(req Request) (Response, error) {
	var resp Response
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// 创建请求体
	var body io.Reader
	if req.Body != "" {
		body = bytes.NewBufferString(req.Body)
	}

	// 创建请求
	httpReq, err := http.NewRequest(req.Method, req.URL, body)
	if err != nil {
		return resp, fmt.Errorf("create request failed: %w", err)
	}

	// 添加头部
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	// 发送请求
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return resp, fmt.Errorf("send request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// 读取响应体
	bodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return resp, fmt.Errorf("read response body failed: %w", err)
	}

	// 构建响应对象
	resp.Proto = httpResp.Proto
	resp.Status = httpResp.Status
	resp.Headers = make(map[string]string)
	for key, values := range httpResp.Header {
		resp.Headers[key] = values[0]
	}
	resp.Body = string(bodyBytes)

	return resp, nil
}
