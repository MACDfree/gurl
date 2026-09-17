package main

import (
	"encoding/json"
	"os"
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
}

var (
	envMu    sync.Mutex
	envOnce  sync.Once
	envCache EnvState
	envPath  = filepath.Join(configDir(), "env.json")
)

func loadEnv() {
	data, err := os.ReadFile(envPath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &envCache)
}

func (h *HTTPService) GetEnvState() (EnvState, error) {
	envOnce.Do(loadEnv)
	envMu.Lock()
	defer envMu.Unlock()
	return envCache, nil
}

func (h *HTTPService) SaveEnvState(state EnvState) error {
	envOnce.Do(loadEnv)
	envMu.Lock()
	defer envMu.Unlock()
	envCache = state
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(envCache)
	if err != nil {
		return err
	}
	return os.WriteFile(envPath, data, 0o644)
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

func subst(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// applyEnv 返回替换过 {{var}} 的请求副本，原始请求（含历史）保持模板。
func applyEnv(req *HTTPRequest) *HTTPRequest {
	vars := envVars()
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
