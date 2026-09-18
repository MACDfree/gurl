package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 超限响应落盘缓存。
//
// 为什么存文件而不是「下载时重发一次请求」：请求可能是非幂等的 POST（再发一次就是
// 重复下单），上游内容也可能不确定；更糟的是用户此刻可能已经改了 URL/body/环境，
// 于是下载到的字节和屏幕上看到的不是同一份——对一个调试工具这是最糟的性质。

const (
	bodyTTL     = 30 * time.Minute
	bodyMaxKeep = 64
)

type bodyEntry struct {
	expiry time.Time
	name   string
}

type bodyCache struct {
	mu    sync.Mutex
	dir   string
	items map[string]bodyEntry
}

var (
	bodiesOnce sync.Once
	bodies     *bodyCache
)

// bodyCacheRef 懒初始化，测试可用 resetBodyCache 换目录。
func bodyCacheRef() *bodyCache {
	bodiesOnce.Do(func() { bodies = newBodyCache() })
	return bodies
}

func newBodyCache() *bodyCache {
	sweepStaleCacheDirs()
	dir, err := os.MkdirTemp("", "gurl-resp-*")
	if err != nil {
		log.Printf("创建响应缓存目录失败，超限响应将只留预览: %v", err)
		return &bodyCache{items: map[string]bodyEntry{}}
	}
	return &bodyCache{dir: dir, items: map[string]bodyEntry{}}
}

// sweepStaleCacheDirs 收拾上次被 SIGKILL 留下的残骸。只删超过 TTL 的，避免误删
// 另一个正在运行的 gurl 实例的活缓存。
func sweepStaleCacheDirs() {
	dirs, _ := filepath.Glob(filepath.Join(os.TempDir(), "gurl-resp-*"))
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && time.Since(fi.ModTime()) > bodyTTL {
			_ = os.RemoveAll(d)
		}
	}
}

func newBodyID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// validBodyID：id 是能力凭证而不是普通键名。ServeMux 的 {id} 段不会跨 '/'，但段内
// 出现 '..' 是合法的，交给 filepath.Join 就会走出缓存目录。
func validBodyID(id string) bool {
	if len(id) != 32 {
		return false
	}
	return strings.TrimFunc(id, func(r rune) bool {
		return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
	}) == ""
}

func (c *bodyCache) Create(nameHint string) (string, *os.File, error) {
	if c.dir == "" {
		return "", nil, errors.New("响应缓存不可用")
	}
	id := newBodyID()
	if id == "" {
		return "", nil, errors.New("生成缓存 id 失败")
	}
	f, err := os.OpenFile(filepath.Join(c.dir, id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", nil, err
	}
	c.mu.Lock()
	c.items[id] = bodyEntry{expiry: time.Now().Add(bodyTTL), name: nameHint}
	c.sweepLocked()
	c.mu.Unlock()
	return id, f, nil
}

func (c *bodyCache) Discard(id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	delete(c.items, id)
	c.mu.Unlock()
	_ = os.Remove(filepath.Join(c.dir, id))
}

// Open 只认合法 id；文件不存在（含服务重启后）返回 os.ErrNotExist。
func (c *bodyCache) Open(id string) (*os.File, error) {
	if c.dir == "" || !validBodyID(id) {
		return nil, fs.ErrNotExist
	}
	c.mu.Lock()
	_, known := c.items[id]
	c.mu.Unlock()
	if !known {
		return nil, fs.ErrNotExist
	}
	return os.Open(filepath.Join(c.dir, id))
}

func (c *bodyCache) Name(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.items[id].name
}

// sweepLocked 在插入时顺带清理：条目少且都有 TTL，不值得为它起后台 goroutine
// （空闲时什么都不做）。
func (c *bodyCache) sweepLocked() {
	now := time.Now()
	for id, e := range c.items {
		if now.After(e.expiry) {
			delete(c.items, id)
			_ = os.Remove(filepath.Join(c.dir, id))
		}
	}
	for len(c.items) > bodyMaxKeep {
		oldest, best := "", bodyEntry{}
		for id, e := range c.items {
			if oldest == "" || e.expiry.Before(best.expiry) {
				oldest, best = id, e
			}
		}
		delete(c.items, oldest)
		_ = os.Remove(filepath.Join(c.dir, oldest))
	}
}

func (c *bodyCache) Close() {
	if c.dir == "" {
		return
	}
	_ = os.RemoveAll(c.dir)
	c.mu.Lock()
	c.items = map[string]bodyEntry{}
	c.mu.Unlock()
}

// downloadName 把请求 URL 变成安全的文件名。只留 ASCII 白名单字符，就不需要
// RFC 5987 的 filename* 编码；前缀 gurl- 便于在下载目录里认出来。
func downloadName(rawURL string) string {
	name := "response"
	if u, err := url.Parse(rawURL); err == nil {
		if base := path.Base(u.Path); base != "." && base != "/" && base != "" {
			name = base
		}
	}
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	out := strings.TrimLeft(b.String(), ".")
	if len(out) > 48 {
		out = out[len(out)-48:]
	}
	if out == "" {
		out = "response"
	}
	return "gurl-" + out
}
