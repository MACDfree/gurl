package main

import (
	"encoding/json"
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
	loadJSON(colPath, &colCache)
}

// cloneCollectionTree 递归脱耦：连节点里的 *HTTPRequest 及其中的切片都要复制，
// 否则调用方改一个请求参数就会写穿到缓存。
func cloneCollectionTree(nodes []*CollectionNode) []*CollectionNode {
	if nodes == nil {
		return nil
	}
	out := make([]*CollectionNode, len(nodes))
	for i, n := range nodes {
		if n == nil {
			continue
		}
		c := *n
		c.Children = cloneCollectionTree(n.Children)
		c.Request = cloneHTTPRequest(n.Request)
		out[i] = &c
	}
	return out
}

func (h *HTTPService) GetCollections() ([]*CollectionNode, error) {
	colOnce.Do(loadCollections)
	colMu.Lock()
	out := cloneCollectionTree(colCache)
	colMu.Unlock()
	return out, nil
}

func (h *HTTPService) SaveCollections(nodes []*CollectionNode) error {
	colOnce.Do(loadCollections)
	colMu.Lock()
	defer colMu.Unlock()
	colCache = nodes
	data, err := json.Marshal(colCache)
	if err != nil {
		return err
	}
	return atomicWrite(colPath, data)
}
