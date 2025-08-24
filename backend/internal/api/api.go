package api

import (
	"fmt"
	"os"

	"gurl/internal/core"
)

// RunTest 从文件运行API测试
func RunTest(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file failed: %w", err)
	}

	// 解析请求内容
	requests, err := core.ParseRequests(string(content))
	if err != nil {
		return fmt.Errorf("parse requests failed: %w", err)
	}

	// 执行请求
	for i, req := range requests {
		fmt.Printf("Running request #%d: %s %s\n", i+1, req.Method, req.URL)
		resp, err := core.SendRequest(req)
		if err != nil {
			fmt.Printf("Request failed: %v\n", err)
			continue
		}

		fmt.Printf("Response status: %s\n", resp.Status)
		fmt.Printf("Response body: %s\n\n", resp.Body)
	}

	return nil
}

func RunTestFromStr(content string) (string, error) {
	// 解析请求内容
	requests, err := core.ParseRequests(content)
	if err != nil {
		return "", fmt.Errorf("parse requests failed: %w", err)
	}

	result := ""
	// 执行请求
	for i, req := range requests {
		result += fmt.Sprintf("Running request #%d: %s %s\n", i+1, req.Method, req.URL)
		resp, err := core.SendRequest(req)
		if err != nil {
			result += fmt.Sprintf("Request failed: %v\n", err)
			continue
		}

		result += fmt.Sprintf("%s %s\n", resp.Proto, resp.Status)
		for key, value := range resp.Headers {
			result += fmt.Sprintf("%s: %s\n", key, value)
		}
		result += "\n"
		result += fmt.Sprintf("%s\n\n", resp.Body)
	}

	return result, nil
}
