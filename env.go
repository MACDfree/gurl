package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
)

type Environment struct {
	Name string `json:"name"`
	Vars []KV   `json:"vars"`
}

type EnvState struct {
	Active string        `json:"active"`
	Envs   []Environment `json:"envs"`
	// Runtime 是响应提取出来的变量：只在内存里，**不落盘**（见 extract.go）。
	// 用指针是为了分清「没提这件事」和「要清空」：界面每次改环境都会整体 PUT 一份
	// {active, envs}，要是把「字段缺席」也当成空表，改个环境名就把运行时变量抹了。
	// 缺席 = 不动，显式给（含空数组）= 整体替换。
	Runtime *[]KV `json:"runtime,omitempty"`
}

var (
	envMu    sync.Mutex
	envOnce  sync.Once
	envCache EnvState
	envPath  = filepath.Join(configDir(), "env.json")
)

func loadEnv() {
	loadJSON(envPath, &envCache)
}

// cloneEnvState 必须连每个环境的 Vars 一起复制：原实现返回结构体值，
// 但 Envs[i].Vars 底层数组仍与缓存共享。
func cloneEnvState(in EnvState) EnvState {
	out := EnvState{Active: in.Active}
	if in.Envs == nil {
		return out
	}
	out.Envs = make([]Environment, len(in.Envs))
	for i, e := range in.Envs {
		out.Envs[i] = Environment{Name: e.Name, Vars: cloneKVs(e.Vars)}
	}
	return out
}

func (h *HTTPService) GetEnvState() (EnvState, error) {
	envOnce.Do(loadEnv)
	envMu.Lock()
	out := cloneEnvState(envCache)
	runtime := runtimeKVs()
	out.Runtime = &runtime
	envMu.Unlock()
	return out, nil
}

func (h *HTTPService) SaveEnvState(state EnvState) error {
	envOnce.Do(loadEnv)
	envMu.Lock()
	defer envMu.Unlock()
	runtime := state.Runtime
	state.Runtime = nil // 运行时变量不进 env.json：它是上一次响应的产物，重启就该没
	envCache = state
	data, err := json.Marshal(envCache)
	if err != nil {
		return err
	}
	if runtime != nil {
		replaceRuntime(*runtime)
	}
	return atomicWrite(envPath, data)
}

func envVars() map[string]string {
	envOnce.Do(loadEnv)
	envMu.Lock()
	defer envMu.Unlock()
	for _, e := range envCache.Envs {
		if e.Name == envCache.Active {
			m := make(map[string]string, len(e.Vars))
			for _, v := range e.Vars {
				if v.Enabled && v.Key != "" {
					m[v.Key] = v.Value
				}
			}
			return m
		}
	}
	return nil
}

// mergedVars 是替换用的最终表：手填的环境变量打底，提取出来的运行时变量盖在上面。
// 谁覆盖谁这件事只在这一处决定，subst 不管语义。
func mergedVars() map[string]string {
	vars := envVars()
	rt := runtimeMap()
	if len(rt) == 0 {
		return vars
	}
	if vars == nil {
		vars = map[string]string{}
	}
	for k, v := range rt {
		vars[k] = v
	}
	return vars
}

func subst(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// applyEnv 返回替换过 {{var}} 的请求副本，原始请求（含历史）保持模板。
func applyEnv(req *HTTPRequest) *HTTPRequest {
	vars := mergedVars()
	if len(vars) == 0 {
		return req
	}
	r := *req
	r.URL = subst(r.URL, vars)
	r.Body = subst(r.Body, vars)
	if len(r.Params) > 0 {
		r.Params = copySubstKVs(r.Params, vars)
	}
	if len(r.Headers) > 0 {
		r.Headers = copySubstKVs(r.Headers, vars)
	}
	if len(r.Form) > 0 {
		r.Form = copySubstKVs(r.Form, vars)
	}
	if len(r.Files) > 0 {
		files := make([]FileKV, len(r.Files))
		for i, f := range r.Files {
			files[i] = FileKV{Key: subst(f.Key, vars), Path: subst(f.Path, vars)}
		}
		r.Files = files
	}
	return &r
}

func copySubstKVs(kvs []KV, vars map[string]string) []KV {
	out := make([]KV, len(kvs))
	for i, kv := range kvs {
		out[i] = KV{Key: subst(kv.Key, vars), Value: subst(kv.Value, vars), Enabled: kv.Enabled}
	}
	return out
}
