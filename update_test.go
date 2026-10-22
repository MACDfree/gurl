package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.3.0", "v0.3.1", -1},
		{"v0.3.1", "v0.3.0", 1},
		{"v0.3.0", "v0.3.0", 0},
		{"v0.9.0", "v0.10.0", -1}, // 数字段比较，不是字符串比较
		{"v1.0.0", "v0.99.99", 1},
		{"0.3.0", "v0.3.1", -1}, // 前缀 v 可有可无
	}
	for _, c := range cases {
		got, err := compareSemver(c.a, c.b)
		if err != nil {
			t.Errorf("compareSemver(%q, %q) 意外报错: %v", c.a, c.b, err)
			continue
		}
		if got != c.want {
			t.Errorf("compareSemver(%q, %q) = %d, 想要 %d", c.a, c.b, got, c.want)
		}
	}
	bad := [][2]string{{"dev", "v0.3.0"}, {"v0.3", "v0.3.0"}, {"v0.3.x", "v0.3.0"}, {"", "v0.3.0"}}
	for _, c := range bad {
		if _, err := compareSemver(c[0], c[1]); err == nil {
			t.Errorf("compareSemver(%q, %q) 应该报错", c[0], c[1])
		}
	}
}

// fakeGitHub 起一个假 GitHub API：/repos/.../releases/latest 回 rel，
// /dl/{name} 回一段 tar.gz（内容是名为 binName 的文件，字节为 payload）。
func fakeGitHub(t *testing.T, status int, rel *ghRelease, payload []byte) *httptest.Server {
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	tw := tar.NewWriter(gw)
	binName := "gurl"
	if runtime.GOOS == "windows" {
		binName = "gurl.exe"
	}
	entries := []struct {
		name string
		body []byte
	}{
		{"README.md", []byte("干扰条目")},
		{binName, payload},
		{"../evil", []byte("路径穿越尝试")},
	}
	for _, e := range entries {
		tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg})
		tw.Write(e.body)
	}
	tw.Close()
	gw.Close()
	bundle := gz.Bytes()

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/MACDfree/gurl/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("请求没带 User-Agent，真实 GitHub 会 403")
		}
		w.WriteHeader(status)
		if rel != nil {
			json.NewEncoder(w).Encode(rel)
		}
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		w.Write(bundle)
	})
	return httptest.NewServer(mux)
}

// releaseJSON 拼一个含当前平台资产的 release；下载地址用相对路径，
// 由 fakeGitHub 的 /dl/ 处理器接住，这样不用先起服务器再回填 URL。
func releaseJSON(tag string) *ghRelease {
	name := assetName(tag)
	return &ghRelease{
		TagName: tag,
		HTMLURL: "https://github.com/MACDfree/gurl/releases/tag/" + tag,
		Assets: []ghAsset{
			{Name: "gurl_" + tag + "_plan9_mips.tar.gz", DownloadURL: "/dl/other"},
			{Name: name, DownloadURL: "/dl/" + name},
		},
	}
}

func newTestUpdater(srvURL, current string) *Updater {
	return &Updater{current: current, apiBase: srvURL}
}

func TestUpdaterCheck(t *testing.T) {
	t.Run("有新版本", func(t *testing.T) {
		srv := fakeGitHub(t, 200, releaseJSON("v0.4.0"), nil)
		defer srv.Close()
		u := newTestUpdater(srv.URL, "v0.3.0")
		info, err := u.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !info.HasUpdate || info.Latest != "v0.4.0" || info.Current != "v0.3.0" {
			t.Errorf("意外的 UpdateInfo: %+v", info)
		}
		if info.ReleaseURL == "" {
			t.Error("应带上 ReleaseURL 兜底")
		}
	})

	t.Run("已是最新", func(t *testing.T) {
		srv := fakeGitHub(t, 200, releaseJSON("v0.3.0"), nil)
		defer srv.Close()
		info, err := newTestUpdater(srv.URL, "v0.3.0").Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.HasUpdate {
			t.Errorf("同版本不该报更新: %+v", info)
		}
	})

	t.Run("dev 构建不猜版本", func(t *testing.T) {
		srv := fakeGitHub(t, 200, releaseJSON("v0.4.0"), nil)
		defer srv.Close()
		u := newTestUpdater(srv.URL, "dev")
		u.disabled = true
		info, err := u.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.HasUpdate || !info.Disabled || info.Latest != "v0.4.0" {
			t.Errorf("dev 构建应只报最新版本不报更新: %+v", info)
		}
	})

	t.Run("仓库没有 release", func(t *testing.T) {
		srv := fakeGitHub(t, 404, nil, nil)
		defer srv.Close()
		info, err := newTestUpdater(srv.URL, "v0.3.0").Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.HasUpdate || info.Latest != "" {
			t.Errorf("404 应视为无更新: %+v", info)
		}
	})

	t.Run("限流", func(t *testing.T) {
		for _, status := range []int{403, 429} {
			srv := fakeGitHub(t, status, nil, nil)
			_, err := newTestUpdater(srv.URL, "v0.3.0").Check(context.Background())
			if err == nil {
				t.Errorf("HTTP %d 应报错", status)
			}
			srv.Close()
		}
	})
}

func TestUpdaterApply(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("替换逻辑依赖 os.Executable 指向测试二进制，windows 的 .old 路径由 replaceExecutable 的分支单独保证")
	}
	payload := []byte("#!/bin/sh\necho new-gurl\n")

	t.Run("Apply 端到端替换", func(t *testing.T) {
		srv := fakeGitHub(t, 200, releaseJSON("v0.4.0"), payload)
		defer srv.Close()

		dir := t.TempDir()
		exe := filepath.Join(dir, "gurl")
		if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		u := newTestUpdater(srv.URL, "v0.3.0")
		u.exePath = exe
		if err := u.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("替换后内容不对: %q", got)
		}
		fi, err := os.Stat(exe)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o111 == 0 {
			t.Errorf("替换后没有可执行位: %v", fi.Mode())
		}
		// 下载用的压缩包临时文件应已清理，目录里只剩替换后的 exe
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 {
			names := []string{}
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("目录里剩了多余文件: %v", names)
		}
	})

	t.Run("坏压缩包不动原文件", func(t *testing.T) {
		rel := releaseJSON("v0.4.0")
		mux := http.NewServeMux()
		mux.HandleFunc("/repos/MACDfree/gurl/releases/latest", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(rel)
		})
		mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("根本不是压缩包"))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		dir := t.TempDir()
		u := newTestUpdater(srv.URL, "v0.3.0")
		if _, err := u.downloadBinary(context.Background(), absoluteURL(srv.URL, rel.Assets[1].DownloadURL), dir); err == nil {
			t.Fatal("坏压缩包应报错")
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Errorf("失败后目录里剩了 %d 个文件", len(entries))
		}
	})

	t.Run("资产缺失报错", func(t *testing.T) {
		rel := &ghRelease{TagName: "v0.4.0", HTMLURL: "https://example/r", Assets: []ghAsset{{Name: "gurl_v0.4.0_plan9_mips.tar.gz", DownloadURL: "http://x/y"}}}
		u := newTestUpdater("http://127.0.0.1:1", "v0.3.0")
		if _, err := u.pickAsset(rel); err == nil {
			t.Fatal("缺当前平台资产应该报错")
		}
	})

	t.Run("dev 构建拒绝 apply", func(t *testing.T) {
		u := newTestUpdater("http://127.0.0.1:1", "dev")
		u.disabled = true
		if err := u.Apply(context.Background()); err == nil {
			t.Fatal("disabled 时 Apply 应报错")
		}
	})
}

func TestMirrorFallback(t *testing.T) {
	payload := []byte("mirror-binary")

	t.Run("主源挂了走镜像", func(t *testing.T) {
		mirror := fakeGitHub(t, 200, releaseJSON("v0.4.0"), payload)
		defer mirror.Close()
		u := newTestUpdater("http://127.0.0.1:9", "v0.3.0") // 9 是 discard 端口，必然连不上
		u.mirrorBase = mirror.URL
		info, err := u.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !info.HasUpdate || info.Latest != "v0.4.0" {
			t.Errorf("应回落到镜像: %+v", info)
		}
	})

	t.Run("主源限流也走镜像", func(t *testing.T) {
		primary := fakeGitHub(t, 403, nil, nil)
		defer primary.Close()
		mirror := fakeGitHub(t, 200, releaseJSON("v0.4.0"), payload)
		defer mirror.Close()
		u := newTestUpdater(primary.URL, "v0.3.0")
		u.mirrorBase = mirror.URL
		info, err := u.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !info.HasUpdate {
			t.Errorf("限流应触发回落: %+v", info)
		}
	})

	t.Run("两边都挂报主源错误", func(t *testing.T) {
		u := newTestUpdater("http://127.0.0.1:9", "v0.3.0")
		u.mirrorBase = "http://127.0.0.1:9"
		if _, err := u.Check(context.Background()); err == nil {
			t.Fatal("应报错")
		}
	})

	t.Run("Apply 走镜像端到端", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("同 TestUpdaterApply")
		}
		mirror := fakeGitHub(t, 200, releaseJSON("v0.4.0"), payload)
		defer mirror.Close()
		dir := t.TempDir()
		exe := filepath.Join(dir, "gurl")
		os.WriteFile(exe, []byte("old"), 0o755)
		u := newTestUpdater("http://127.0.0.1:9", "v0.3.0")
		u.mirrorBase = mirror.URL
		u.exePath = exe
		if err := u.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(exe)
		if !bytes.Equal(got, payload) {
			t.Errorf("替换后内容不对: %q", got)
		}
	})
}

func TestReplaceExecutableWindowsRenameOrder(t *testing.T) {
	// 平台无关地验证 replaceExecutable 的 unix 分支语义：目标是普通文件时原子顶掉
	if runtime.GOOS == "windows" {
		t.Skip("unix 分支")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "app")
	fresh := filepath.Join(dir, "fresh")
	os.WriteFile(target, []byte("old"), 0o755)
	os.WriteFile(fresh, []byte("new"), 0o755)
	if err := replaceExecutable(target, fresh); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new" {
		t.Errorf("target = %q, 想要 new", got)
	}
}
