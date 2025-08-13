package core

import (
	"fmt"
	"regexp"
	"strings"
)

// Request 表示一个HTTP请求
type Request struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// Response 表示一个HTTP响应
type Response struct {
	Status  string            `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

var blockSplitRegex = regexp.MustCompile(`#{3,}\S*\n`)
var methodURLSplitRegex = regexp.MustCompile(` +`)

// ParseRequests 解析请求内容
func ParseRequests(content string) ([]Request, error) {
	// 将content中的换行符统一为\n
	content = strings.ReplaceAll(content, "\r\n", "\n")
	var requests []Request
	blocks := blockSplitRegex.Split(content, -1)

	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		req, err := parseRequestBlock(block)
		if err != nil {
			return nil, err
		}

		requests = append(requests, req)
	}

	return requests, nil
}

// parseRequestBlock 解析单个请求块
func parseRequestBlock(block string) (Request, error) {
	var req Request
	lines := strings.Split(block, "\n")

	// 解析第一行: METHOD URL
	firstLine := strings.TrimSpace(lines[0])
	parts := strings.SplitN(firstLine, " ", 2)
	if len(parts) < 2 {
		return req, fmt.Errorf("invalid request format: %s", firstLine)
	}

	req.Method = strings.ToUpper(parts[0])
	req.URL = parts[1]
	req.Headers = make(map[string]string)

	// 解析头部和正文
	bodyStart := 1
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			bodyStart = i + 1
			break
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			headName := strings.TrimSpace(parts[0])
			headValue := strings.TrimSpace(parts[1])
			//TODO 需要对headName和headValue进行校验
			req.Headers[headName] = headValue
		} else {
			return req, fmt.Errorf("invalid request format: %s", line)
		}
		bodyStart = i + 1
	}

	// 解析正文
	if bodyStart < len(lines) {
		bodyLines := lines[bodyStart:]
		req.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
	}

	return req, nil
}
