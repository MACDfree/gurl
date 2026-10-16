package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	host := flag.String("host", "127.0.0.1", "监听地址；保持回环，跨机使用时文件路径等能力会失效")
	port := flag.String("port", "0", "监听端口，0 表示由内核挑一个空闲端口")
	openFlag := flag.Bool("open", true, "启动时自动打开默认浏览器")
	dev := flag.Bool("dev", false, "打印每个请求的访问日志")
	allowOrigin := flag.String("allow-origin", "", "额外放行 /api 的主机名，逗号分隔（域名、IP 或完整 URL）；反代或跨机访问时填浏览器实际用的地址")
	flag.Parse()

	bodyCacheRef() // 建缓存目录，同时清掉上次被 SIGKILL 留下的残骸
	defer bodyCacheRef().Close()

	// 绑 :0 再从 listener 读回端口。「先扫空闲端口再绑定」是和别的进程抢端口的 TOCTOU 竞争。
	ln, err := net.Listen("tcp", net.JoinHostPort(*host, *port))
	if err != nil {
		log.Fatalf("监听失败: %v", err)
	}
	displayHost := *host
	if isUnspecified(displayHost) {
		displayHost = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s:%d/", displayHost, ln.Addr().(*net.TCPAddr).Port)

	srv := &http.Server{
		Handler: newServer(&HTTPService{}, *host, parseAllow(*allowOrigin), *dev, distFS()),
		// 故意不设 ReadTimeout/WriteTimeout：Send 会占住 response writer 长达 timeoutMs
		// （默认 30s，用户可设更大），常规的 WriteTimeout 会静默掐断长请求。
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	log.Printf("gurl 已启动：%s", url)
	if *openFlag {
		if err := openBrowser(url); err != nil {
			log.Printf("自动打开浏览器失败，请手动访问上面的地址: %v", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		log.Fatalf("服务异常退出: %v", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("优雅退出超时，仍有请求在途: %v", err)
		}
	}
}

func distFS() fs.FS {
	sub, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		panic(err) // embed 路径由编译期决定，不可能失败
	}
	return sub
}
