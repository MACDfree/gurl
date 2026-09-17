package main

import (
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

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return os.TempDir()
	}
	return filepath.Join(dir, "gurl")
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
	if req != nil && req.URL != "" {
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
	return resp, err
}
