package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Updater 负责检查 GitHub 上的最新 Release 并把当前二进制替换成新版。
//
// 只做「提示 + 用户确认后自替换重启」，不做静默更新：gurl 是单二进制本地工具，
// 替换失败最多退回手动下载，不值得更复杂的机制。
type Updater struct {
	current    string // 当前版本号；CI 用 -X main.version 注入，开发构建是 "dev"
	disabled   bool   // dev 构建或 -dev 模式：check 仍可用（报告最新版本），apply 直接拒绝
	port       int    // 实际绑定端口，重启子进程要传回去，页面才能原地续上
	apiBase    string // 默认 https://api.github.com；env GURL_UPDATE_API 可覆盖（仅本地联调用）
	mirrorBase string // 自建镜像，GitHub 请求失败时回落；env GURL_UPDATE_MIRROR 可覆盖（仅本地联调用）
	exePath    string // 要替换的可执行文件；空表示用 exeRealPath() 现算（测试注入假路径）
	restart    func() // apply 成功后调用：main.go 注入，负责 shutdown → 起新进程 → 退出
}

// defaultMirror 是自建发布镜像：GitHub API 在国内经常不可达，请求失败时回落到它。
// 镜像由 scripts/mirror-sync.sh 从 GitHub Releases 同步，目录结构与 GitHub API 一致。
const defaultMirror = "http://sharefile.macdfree.cc:8816/gurl"

// newUpdater 组装更新器并注入重启逻辑。srv 在调用方还没赋完值，restart 是闭包、
// 只在 apply 成功后才跑，届时 srv 已就绪。
func newUpdater(current string, devFlag bool, port int, srv *http.Server) *Updater {
	apiBase := os.Getenv("GURL_UPDATE_API") // dev-only：本地联调指向假 GitHub API
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	mirror := os.Getenv("GURL_UPDATE_MIRROR") // dev-only：本地联调指向假镜像
	if mirror == "" {
		mirror = defaultMirror
	}
	u := &Updater{
		current:    current,
		disabled:   devFlag || current == "dev",
		port:       port,
		apiBase:    strings.TrimSuffix(apiBase, "/"),
		mirrorBase: strings.TrimSuffix(mirror, "/"),
	}
	u.restart = func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			srv.Shutdown(ctx) // 先释放端口，新进程才绑得上同一个
			exe, err := exeRealPath()
			if err != nil {
				log.Printf("自更新后重启失败: %v", err)
				os.Exit(1)
			}
			// Go flag 后出现的覆盖先出现的：追加的 -port/-open 一定生效，
			// 端口不变，浏览器页面轮询到新进程后原地 reload 即可。
			args := append(append([]string{}, os.Args[1:]...),
				"-port="+strconv.Itoa(u.port), "-open=false")
			cmd := exec.Command(exe, args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				log.Printf("自更新后重启失败: %v", err)
				os.Exit(1)
			}
			os.Exit(0)
		}()
	}
	return u
}

// UpdateInfo 是 GET /api/update/check 的响应体。
type UpdateInfo struct {
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	HasUpdate  bool   `json:"hasUpdate"`
	Disabled   bool   `json:"disabled"`
	ReleaseURL string `json:"releaseUrl"`
}

type ghAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	HTMLURL string    `json:"html_url"`
	Assets  []ghAsset `json:"assets"`
}

// fetchLatestFrom 从指定源（GitHub API 或形状相同的自建镜像）拉最新 Release。
// GitHub 要求 User-Agent，缺了直接 403。/releases/latest 本身排除 prerelease 和 draft。
func (u *Updater) fetchLatestFrom(ctx context.Context, base string) (*ghRelease, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/MACDfree/gurl/releases/latest", nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "gurl")
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, false, nil // 仓库还没有任何 release
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, false, errors.New("GitHub API 请求被限流，请稍后再试")
	default:
		return nil, false, fmt.Errorf("GitHub API 返回 HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, false, err
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, false, errors.New("GitHub API 响应不是合法 JSON")
	}
	// 资产地址统一成绝对 URL：测试夹具用相对路径，镜像/真实 GitHub 是绝对的
	for i := range rel.Assets {
		rel.Assets[i].DownloadURL = absoluteURL(base, rel.Assets[i].DownloadURL)
	}
	return &rel, true, nil
}

func absoluteURL(base, u string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return base + u
}

func (u *Updater) Check(ctx context.Context) (*UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rel, ok, err := u.fetchLatestFrom(ctx, u.apiBase)
	if err != nil || !ok {
		if mrel, mok, merr := u.fetchLatestFromMirror(ctx, err); mok && merr == nil {
			rel, ok = mrel, mok
		} else if err != nil {
			return nil, err
		}
	}
	info := &UpdateInfo{Current: u.current, Disabled: u.disabled}
	if !ok {
		return info, nil
	}
	info.Latest = rel.TagName
	info.ReleaseURL = rel.HTMLURL
	// 当前版本不是合法 semver（dev 构建）或比较出错时不猜，只报最新版本让用户自己判断
	cmp, err := compareSemver(u.current, rel.TagName)
	info.HasUpdate = err == nil && cmp < 0
	return info, nil
}

// fetchLatestFromMirror 在 GitHub 请求失败或仓库无 release 时试一下自建镜像。
// 镜像没配置或与主源相同时直接放弃。
func (u *Updater) fetchLatestFromMirror(ctx context.Context, primaryErr error) (*ghRelease, bool, error) {
	if u.mirrorBase == "" || u.mirrorBase == u.apiBase {
		return nil, false, primaryErr
	}
	return u.fetchLatestFrom(ctx, u.mirrorBase)
}

// Apply 下载当前平台的 Release 资产，替换正在运行的二进制。
// 替换成功后返回，由端点处理器写响应、再触发 restart。
func (u *Updater) Apply(ctx context.Context) error {
	if u.disabled {
		return errors.New("开发构建不支持自更新，请手动下载")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if _, err := parseSemver(u.current); err != nil {
		return fmt.Errorf("当前版本 %q 无法比较，不支持自更新", u.current)
	}
	// GitHub 请求失败就换镜像重来：混着用容易出现「GitHub 的元数据 + 镜像的包」
	// 这种半吊子组合，排查起来更费劲。镜像给出了明确结论（哪怕是「已是最新」）
	// 就采纳它；镜像本身也挂了才报主源的错误
	rel, ok, err := u.fetchLatestFrom(ctx, u.apiBase)
	if err != nil || !ok {
		if u.mirrorBase != "" && u.mirrorBase != u.apiBase {
			if mrel, mok, merr := u.fetchLatestFrom(ctx, u.mirrorBase); merr == nil && mok {
				return u.applyRelease(ctx, mrel)
			}
		}
		if err != nil {
			return err
		}
		return errors.New("仓库还没有任何 Release")
	}
	return u.applyRelease(ctx, rel)
}

func (u *Updater) applyRelease(ctx context.Context, rel *ghRelease) error {
	if cmp, err := compareSemver(u.current, rel.TagName); err != nil || cmp >= 0 {
		return errors.New("当前已是最新版本")
	}
	url, err := u.pickAsset(rel)
	if err != nil {
		return err
	}
	exePath := u.exePath
	if exePath == "" {
		if exePath, err = exeRealPath(); err != nil {
			return err
		}
	}
	newBin, err := u.downloadBinary(ctx, url, filepath.Dir(exePath))
	if err != nil {
		return err
	}
	// unix 成功路径上 newBin 已被 rename 成 exe，这里是 no-op；
	// windows 成功路径和所有失败路径负责把 newBin 清掉
	defer os.Remove(newBin)
	return replaceExecutable(exePath, newBin)
}

// assetName 按 release.yml 里固定的命名规则拼当前平台的资产名。
func assetName(tag string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("gurl_%s_windows_%s.zip", tag, runtime.GOARCH)
	}
	return fmt.Sprintf("gurl_%s_%s_%s.tar.gz", tag, runtime.GOOS, runtime.GOARCH)
}

func (u *Updater) pickAsset(rel *ghRelease) (string, error) {
	want := assetName(rel.TagName)
	for _, a := range rel.Assets {
		if a.Name == want {
			return a.DownloadURL, nil
		}
	}
	return "", fmt.Errorf("Release %s 没有 %s/%s 的包，请到 %s 手动下载", rel.TagName, runtime.GOOS, runtime.GOARCH, rel.HTMLURL)
}

// downloadBinary 下载资产并从压缩包里提取二进制，返回新文件路径（dir 下随机名）。
// dir 必须和目标可执行文件同目录：同文件系统，替换时的 rename 才是原子的。
func (u *Updater) downloadBinary(ctx context.Context, url, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "gurl")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载更新失败：HTTP %d", res.StatusCode)
	}
	var binPath string
	tmp, err := os.CreateTemp(dir, "gurl-update-*")
	if err != nil {
		return "", err
	}
	// 失败时清掉这个空壳临时文件；成功时它就是返回的新二进制，由调用方处置
	defer func() {
		tmp.Close()
		if binPath == "" {
			os.Remove(tmp.Name())
		}
	}()
	binPath, err = extractBinary(res.Body, dir, filepath.Base(tmp.Name()), strings.HasSuffix(url, ".zip"))
	if err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return binPath, nil
}

// extractBinary 从压缩流里提取唯一认名的二进制条目，写进 dir（= 目标 exe 同目录）
// 下的临时文件并设 0755，返回其路径。压缩包内容视为不可信：只接受名字恰等于
// gurl / gurl.exe 的条目，写入位置由我们自己的 CreateTemp 决定，杜绝路径穿越。
func extractBinary(r io.Reader, dir, tmpBase string, isZip bool) (string, error) {
	want := "gurl"
	if runtime.GOOS == "windows" {
		want = "gurl.exe"
	}
	if isZip {
		data, err := io.ReadAll(io.LimitReader(r, 300<<20))
		if err != nil {
			return "", err
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return "", errors.New("更新的 zip 包损坏")
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == want {
				rc, err := f.Open()
				if err != nil {
					return "", err
				}
				defer rc.Close()
				return writeExtracted(rc, dir, tmpBase)
			}
		}
		return "", errors.New("更新的 zip 包里没有找到 " + want)
	}

	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", errors.New("更新的压缩包损坏")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", errors.New("更新的压缩包损坏")
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == want {
			return writeExtracted(tr, dir, tmpBase)
		}
	}
	return "", errors.New("更新的压缩包里没有找到 " + want)
}

func writeExtracted(src io.Reader, dir, tmpBase string) (string, error) {
	path := filepath.Join(dir, tmpBase)
	// 文件已由 CreateTemp 建出来（0600），OpenFile 不会改已存在文件的权限，得显式 Chmod
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, io.LimitReader(src, 300<<20)); err != nil {
		out.Close()
		return "", errors.New("写入新程序失败")
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// replaceExecutable 用 newBin 顶掉 exePath。
// unix 下直接 rename 覆盖运行中的自身：旧 inode 还被进程持有，安全。
// windows 不允许覆盖运行中的 exe，先把自己改名成 .old 腾位置，下次启动时清理；
// 第二步失败就把 .old 改回来，保证怎么失败原二进制都还在。
func replaceExecutable(exePath, newBin string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(newBin, exePath)
	}
	old := exePath + ".old"
	os.Remove(old)
	if err := os.Rename(exePath, old); err != nil {
		return fmt.Errorf("无法替换当前程序（%v），请手动下载", err)
	}
	if err := os.Rename(newBin, exePath); err != nil {
		os.Rename(old, exePath) // 回滚
		return fmt.Errorf("无法放入新程序（%v），请手动下载", err)
	}
	return nil
}

// cleanupOldExecutable 启动时 best-effort 清掉上次 windows 更新留下的 .old。
func cleanupOldExecutable() {
	if runtime.GOOS != "windows" {
		return
	}
	if p, err := exeRealPath(); err == nil {
		os.Remove(p + ".old")
	}
}

func exeRealPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// 经由 symlink 启动时替换 symlink 本身没有意义，要顶掉真实文件
	p, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return p, nil
}

// compareSemver 比较两个 vX.Y.Z 形状的版本号，a<b 返回 -1，相等 0，a>b 返回 1。
// 任何一段解析不了就报错，调用方按「无法判断，视为无更新」处理。
func compareSemver(a, b string) (int, error) {
	pa, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
}

func parseSemver(v string) ([3]int, error) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("版本号 %q 不是 X.Y.Z 形状", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("版本号 %q 含非数字段", v)
		}
		out[i] = n
	}
	return out, nil
}
