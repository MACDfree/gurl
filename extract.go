package main

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// 从响应里提取值存成「运行时变量」，下一个请求的 {{名字}} 就能用上——
// 「先登录拿 token，再调别的接口」这条链不必手工复制粘贴。
//
// 为什么不直接写回环境文件：环境里是用户手填的值（baseURL 之类），让每次响应去覆盖它，
// 一旦路径写错或上游改了结构，就会把人肉配置悄悄改坏（而且提取不到时是「静默没值」）。
// 运行时变量只在内存里，替换时盖在同名环境变量上面，重启即失效，界面上单独可见。

// ExtractRule 一条提取规则。
type ExtractRule struct {
	Name   string `json:"name"`
	Source string `json:"source"` // body | header；空按 body
	// Path：body 是点/下标路径（data.token、items[0].id，可带 $. 前缀）；header 是头名。
	Path string `json:"path"`
}

var (
	rtMu   sync.Mutex
	rtVars = map[string]string{}
)

func runtimeMap() map[string]string {
	rtMu.Lock()
	defer rtMu.Unlock()
	out := make(map[string]string, len(rtVars))
	for k, v := range rtVars {
		out[k] = v
	}
	return out
}

// runtimeKVs 按名字排序返回，免得前端每次刷新都看到不同的顺序。
func runtimeKVs() []KV {
	m := runtimeMap()
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]KV, 0, len(names))
	for _, k := range names {
		out = append(out, KV{Key: k, Value: m[k], Enabled: true})
	}
	return out
}

func setRuntime(name, value string) {
	rtMu.Lock()
	rtVars[name] = value
	rtMu.Unlock()
}

// replaceRuntime 整体替换（PUT /api/env 带上 runtime 时用于「清空」）。
func replaceRuntime(kvs []KV) {
	next := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		if kv.Enabled && kv.Key != "" {
			next[kv.Key] = kv.Value
		}
	}
	rtMu.Lock()
	rtVars = next
	rtMu.Unlock()
}

// applyExtract 按规则取值；取到的写进运行时变量，并原样回给前端（取不到的不出现，
// 前端拿规则和这个 map 一比就知道哪条没命中——不必再开一条错误字段）。
func applyExtract(req *HTTPRequest, resp *HTTPResponse) map[string]string {
	if len(req.Extract) == 0 || resp == nil {
		return nil
	}
	var decoded any
	var haveBody bool
	out := map[string]string{}
	for _, r := range req.Extract {
		name := strings.TrimSpace(r.Name)
		path := strings.TrimSpace(r.Path)
		if name == "" || path == "" {
			continue
		}
		var (
			val string
			ok  bool
		)
		if r.Source == "header" {
			val, ok = lookupHeader(resp.Headers, path)
		} else {
			if !haveBody {
				haveBody = true
				if strings.TrimSpace(resp.Body) != "" {
					_ = json.Unmarshal([]byte(resp.Body), &decoded)
				}
			}
			val, ok = lookupPath(decoded, path)
		}
		if !ok {
			continue
		}
		setRuntime(name, val)
		out[name] = val
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func lookupHeader(hs []KV, name string) (string, bool) {
	for _, h := range hs {
		if strings.EqualFold(h.Key, name) {
			return h.Value, true
		}
	}
	return "", false
}

// lookupPath 走一条 a.b[0].c 形式的路径。标量给原值，对象/数组给紧凑 JSON——
// 提取一整段结构再放进下一个请求的 body 里也是常见用法。
func lookupPath(doc any, path string) (string, bool) {
	if doc == nil {
		return "", false
	}
	cur, ok := pathValue(doc, path)
	if !ok {
		return "", false
	}
	switch v := cur.(type) {
	case string:
		return v, true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(v), true
	case nil:
		return "", true
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", false
		}
		return string(b), true
	}
}

type pathSeg struct {
	key   string
	index int // -1 表示键
}

func splitPath(path string) []pathSeg {
	p := strings.TrimPrefix(strings.TrimSpace(path), "$")
	p = strings.TrimPrefix(p, ".")
	var segs []pathSeg
	for _, part := range strings.Split(p, ".") {
		if part == "" {
			continue
		}
		for {
			open := strings.IndexByte(part, '[')
			if open < 0 {
				segs = append(segs, pathSeg{key: part, index: -1})
				break
			}
			if open > 0 {
				segs = append(segs, pathSeg{key: part[:open], index: -1})
			}
			closed := strings.IndexByte(part[open:], ']')
			if closed < 0 {
				// 方括号没闭合：剩下的整段当键名，别丢
				segs = append(segs, pathSeg{key: part[open+1:], index: -1})
				break
			}
			inside := part[open+1 : open+closed]
			if idx, err := strconv.Atoi(inside); err == nil {
				segs = append(segs, pathSeg{index: idx})
			} else {
				segs = append(segs, pathSeg{key: inside, index: -1})
			}
			part = part[open+closed+1:]
			if part == "" {
				break
			}
			part = strings.TrimPrefix(part, ".")
		}
	}
	return segs
}

func pathValue(doc any, path string) (any, bool) {
	cur := doc
	for _, seg := range splitPath(path) {
		if seg.index >= 0 {
			arr, ok := cur.([]any)
			if !ok || seg.index >= len(arr) {
				return nil, false
			}
			cur = arr[seg.index]
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := obj[seg.key]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}
