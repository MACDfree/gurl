package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// atomicWrite 先写同目录下的临时文件，再 rename 过去。
//
// 不能直接用 os.WriteFile：它是原地截断写，进程在写一半时被 Ctrl-C 或崩溃，就会留下
// 半截 JSON；而各 loadXxx 原本用 `_ = json.Unmarshal` 静默吞掉解析失败，最终表现是
// 「用户的集合全没了」。同目录 rename 在同一文件系统上是原子的。
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".gurl-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // rename 成功后这里就是空操作
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// loadJSON 读配置文件。解析失败必须喊出来：静默当成空配置，用户看到的就是数据凭空消失。
func loadJSON(path string, v any) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if err := json.Unmarshal(data, v); err != nil {
		log.Printf("解析 %s 失败（已按空配置继续，原始文件未改动）: %v", path, err)
	}
}
