package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type CollectionNode struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"` // collection | folder | request
	Children []*CollectionNode `json:"children,omitempty"`
	Request  *HTTPRequest      `json:"request,omitempty"`
}

var (
	colMu    sync.Mutex
	colOnce  sync.Once
	colCache []*CollectionNode
	colPath  = filepath.Join(configDir(), "collections.json")
)

func loadCollections() {
	data, err := os.ReadFile(colPath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &colCache)
}

func (h *HTTPService) GetCollections() ([]*CollectionNode, error) {
	colOnce.Do(loadCollections)
	colMu.Lock()
	defer colMu.Unlock()
	return colCache, nil
}

func (h *HTTPService) SaveCollections(nodes []*CollectionNode) error {
	colOnce.Do(loadCollections)
	colMu.Lock()
	defer colMu.Unlock()
	colCache = nodes
	if err := os.MkdirAll(filepath.Dir(colPath), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(colCache)
	if err != nil {
		return err
	}
	return os.WriteFile(colPath, data, 0o644)
}
