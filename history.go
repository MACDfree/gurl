package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const historyLimit = 100

type HistoryEntry struct {
	Time       string      `json:"time"`
	Request    HTTPRequest `json:"request"`
	Status     int         `json:"status"`
	DurationMS int64       `json:"durationMs"`
	Size       int64       `json:"size"`
	Error      string      `json:"error"`
}

var (
	historyMu    sync.Mutex
	historyOnce  sync.Once
	historyCache []HistoryEntry
	historyPath  = filepath.Join(configDir(), "history.json")
)

// configDir 数据跟可执行文件放一起：整个目录拷走就是完整迁移。
//
// GURL_DATA_DIR 存在的唯一理由是 `go run .`：那时 exe 在 Go 的编译缓存里，每改一次代码
// 就换一个目录，历史跟着丢。软链要解引用，否则 /usr/local/bin/gurl 会把数据写进 /usr/local/bin。
func configDir() string {
	if dir := os.Getenv("GURL_DATA_DIR"); dir != "" {
		return dir
	}
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

func loadHistory() {
	loadJSON(historyPath, &historyCache)
}

func appendHistory(e HistoryEntry) {
	historyOnce.Do(loadHistory)
	historyMu.Lock()
	defer historyMu.Unlock()
	historyCache = append([]HistoryEntry{e}, historyCache...)
	if len(historyCache) > historyLimit {
		historyCache = historyCache[:historyLimit]
	}
	saveHistoryLocked()
}

func saveHistoryLocked() {
	data, err := json.Marshal(historyCache)
	if err != nil {
		return
	}
	_ = atomicWrite(historyPath, data)
}

func (h *HTTPService) GetHistory() ([]HistoryEntry, error) {
	historyOnce.Do(loadHistory)
	historyMu.Lock()
	out := make([]HistoryEntry, len(historyCache))
	for i, e := range historyCache {
		// 浅拷贝会让每条历史的 Request 与缓存共享底层切片。
		e.Request = cloneRequest(e.Request)
		out[i] = e
	}
	historyMu.Unlock()
	return out, nil
}

func (h *HTTPService) ClearHistory() error {
	historyOnce.Do(loadHistory)
	historyMu.Lock()
	defer historyMu.Unlock()
	historyCache = nil
	if err := os.Remove(historyPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Send 记录历史后返回结果，空 URL 的请求不记录。
func (h *HTTPService) Send(req *HTTPRequest) (*HTTPResponse, error) {
	resp, err := h.send(req)
	recordHistory(req, resp, err)
	return resp, err
}

// SendStream 同上，只是结果通过 emit 逐帧交出去。
func (h *HTTPService) SendStream(ctx context.Context, req *HTTPRequest, emit func(streamFrame) error) (*HTTPResponse, error) {
	resp, err := h.sendStream(ctx, req, emit)
	if ctx.Err() != nil {
		// 浏览器先走的（点停止、关标签页）不算这次请求失败：中断一个流是正常操作，
		// 不该在历史里留一笔红的。
		err = nil
	}
	recordHistory(req, resp, err)
	return resp, err
}

// recordHistory 出错也要留一条：历史里看不到「试过了、失败了」，就等于没有历史。
func recordHistory(req *HTTPRequest, resp *HTTPResponse, err error) {
	if req == nil || req.URL == "" {
		return
	}
	entry := HistoryEntry{
		Time:    time.Now().Format(time.RFC3339),
		Request: cloneRequest(*req),
	}
	if resp != nil {
		entry.Status = resp.Status
		entry.DurationMS = resp.DurationMS
		entry.Size = resp.Size
	}
	if err != nil {
		entry.Error = err.Error()
	}
	appendHistory(entry)
}
