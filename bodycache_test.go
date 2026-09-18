package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// resetBodyCache 与 resetHistory/resetEnvCol 同一套路：改包级变量 + 重置 sync.Once。
// 因此这些测试不可并行。
func resetBodyCache(t *testing.T) {
	t.Helper()
	bodiesOnce = sync.Once{}
	bodies = &bodyCache{dir: t.TempDir(), items: map[string]bodyEntry{}}
}

func TestBodyCacheRoundTrip(t *testing.T) {
	resetBodyCache(t)
	c := bodyCacheRef()

	id, f, err := c.Create("login.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("payload"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := c.Open(id)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	if name := c.Name(id); name != "login.json" {
		t.Errorf("文件名 = %q（前缀由 downloadName 负责，Create 只原样存提示名）", name)
	}

	c.Discard(id)
	if _, err := c.Open(id); err == nil {
		t.Error("Discard 之后仍可读")
	}
}

// id 是能力凭证。'..' 在单个路径段内是合法的，交给 filepath.Join 就会走出缓存目录。
func TestBodyCacheRejectsBadIDs(t *testing.T) {
	resetBodyCache(t)
	c := bodyCacheRef()
	for _, id := range []string{"", "x", strings.Repeat("a", 31), strings.Repeat("A", 32), "../../etc/passwd"} {
		if _, err := c.Open(id); err == nil {
			t.Errorf("非法 id 被接受: %q", id)
		}
	}
	if !validBodyID(strings.Repeat("0a1f", 8)) {
		t.Error("合法 id 被拒")
	}
}

func TestBodyCacheEviction(t *testing.T) {
	resetBodyCache(t)
	c := bodyCacheRef()
	for i := 0; i < bodyMaxKeep+20; i++ {
		_, f, err := c.Create("x")
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	c.mu.Lock()
	n := len(c.items)
	c.mu.Unlock()
	if n > bodyMaxKeep {
		t.Errorf("条目数 %d 超过上限 %d", n, bodyMaxKeep)
	}
}

func TestBodyCacheTTLExpiry(t *testing.T) {
	resetBodyCache(t)
	c := bodyCacheRef()
	id, f, err := c.Create("x")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	c.mu.Lock()
	e := c.items[id]
	e.expiry = time.Now().Add(-time.Minute)
	c.items[id] = e
	c.mu.Unlock()

	// 清扫由流量触发：下一次插入顺带回收过期条目，不为它起后台 goroutine。
	_, f2, err := c.Create("y")
	if err != nil {
		t.Fatal(err)
	}
	f2.Close()
	if _, err := c.Open(id); err == nil {
		t.Error("过期条目未被回收")
	}
}

func TestDownloadName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"http://x/api/login", "gurl-login"},
		{"http://x/api/report.csv", "gurl-report.csv"},
		{"http://x/", "gurl-response"},
		{"http://x/a/../../etc/passwd", "gurl-passwd"},
		{"http://x/中文.json", "gurl-json"}, // 非 ASCII 被白名单滤掉，剩下 .json
		{"http://x/" + strings.Repeat("a", 60) + ".bin", "gurl-" + strings.Repeat("a", 44) + ".bin"},
	} {
		if got := downloadName(tc.in); got != tc.want {
			t.Errorf("downloadName(%q) = %q, 期望 %q", tc.in, got, tc.want)
		}
	}
}

// sweepStaleCacheDirs 只删超过 TTL 的残骸，避免误删另一个正在运行的 gurl 实例的活缓存。
func TestStaleSweepKeepsFreshDirs(t *testing.T) {
	base := t.TempDir()
	t.Setenv("TMPDIR", base) // os.TempDir 在 darwin/linux 上读这个变量

	fresh := filepath.Join(base, "gurl-resp-fresh")
	stale := filepath.Join(base, "gurl-resp-stale")
	for _, d := range []string{fresh, stale} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * bodyTTL)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	sweepStaleCacheDirs()

	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("新目录被误删: %v", err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("过期残骸未被清理")
	}
}
