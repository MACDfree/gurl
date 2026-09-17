package main

import (
	"os/exec"
	"runtime"
)

// openBrowser 用系统默认浏览器打开 URL。不经过 shell，避免任何引号/& 注入。
func openBrowser(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{url}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		name, args = "xdg-open", []string{url}
	}
	return exec.Command(name, args...).Start()
}
