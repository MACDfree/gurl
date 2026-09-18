# gurl

一个跑在浏览器里的 API 调试工具：单二进制起本地服务，界面在浏览器标签页里。
支持 Params / Headers / Body（JSON、raw、urlencoded、form-data 含文件上传）、请求体 gzip、
响应解压与预览、历史记录、环境变量 `{{var}}`、集合树（可拖拽排序）。

## 跑起来

```bash
make build          # 前端产物 embed 进单二进制 → bin/gurl
./bin/gurl          # 起服务并自动打开浏览器，终端会打印带 token 的 URL
make dev            # 开发模式：后端 127.0.0.1:9246 + Vite 127.0.0.1:9245
make test           # go test -race
make vet / make tidy / make clean
```

开发时打开 `http://127.0.0.1:9245/`（不要带 `?t=`，token 由 Vite 代理注入）。
前端改动即时热更新；**Go 改动需要手动重启后端**。

## 结构

```
main.go            监听 :0（内核挑端口）、生成会话 token、开浏览器、优雅退出
server.go          中间件栈 + 路由；handlers.go  9 个 JSON 端点
auth.go            Bearer token 校验（仅下载端点允许 ?t=）、Origin/Host 纵深防御
static.go          embed 的前端产物 + 缓存头 + 窄化的 SPA 兜底
requestservice.go  发请求、解压、双上限预览
bodycache.go       超限响应落盘缓存（供「下载完整响应」）
history/env/collections.go   配置持久化（~/Library/Application Support/gurl）
frontend/          Vue 3 + TS + Vite + Naive UI + CodeMirror 6
```

API 一律返回 `{"ok":true,"data":…}` / `{"ok":false,"error":"中文原因"}`；
服务层错误走 HTTP 500，前端因此只有一条失败分支，不存在「静默失败」。

## 为什么有 token

服务只绑 127.0.0.1，但本机任意进程、任意网页都能打到回环。没有密钥时 gurl 就是一个
「可发任意 HTTP 请求、还会写你配置文件」的中继。Go 启动时生成随机 token、用
`?t=…` 打开浏览器，前端存进 sessionStorage 后把地址栏抹干净，之后所有请求带
`Authorization: Bearer`。

不加任何 CORS：跨源页面只能免预检发「简单请求」，而这里要求 Authorization 头、
或 JSON 体、或 PUT/DELETE——任意一条都强制预检，零 CORS 头即失败关闭。
**因此「写端点必须收 JSON 体」是 CSRF 防线，不是代码风格。**

## 已知限制

1. **后端必须与浏览器在同一台机器**。文件选择框返回的是服务端绝对路径，远程访问
   （如 `ssh -L`）下 `os.Open` 读的是本机磁盘，会静默传错文件。
2. **多标签页并发编辑会互相覆盖**：集合/环境是全量读-改-写。已做 400ms 合并写 +
   关页面前冲刷，跨标签页的乐观并发（版本号 + 409）尚未实现。
3. **不是单实例**：起两个 gurl 会占两个端口、写同一份配置文件。写入已是原子的
   （临时文件 + rename），不会再出现「半截 JSON 导致集合看起来全空」，但两边会互相覆盖。
4. **macOS 的文件对话框走 osascript**（[ncruces/zenity](https://github.com/ncruces/zenity)，
   cgo-free）：对话框可能开在终端窗口后面；选桌面/文稿/下载会触发归属到终端的 TCC 授权；
   装了 MDM/Santa 的环境可能直接拦掉，此时错误串会原样显示。
5. **iCloud 未下载的文件**会成功打开但只读到几百字节的占位桩，上传前留意文件大小。
6. **响应体上限**：预览 5 MiB（`previewCap`），落盘缓存 64 MiB（`hardCap`）。超过 5 MiB
   的部分不进 DOM，走「下载完整响应」；超过 64 MiB 不缓存。常量都在 `requestservice.go`。
7. **HTTP/1.1 下浏览器同源并发上限是 6**：一个 30s 的长请求 + 几个面板加载可能排队。
   开发模式下 Vite 代理还可能先于超长 `timeoutMs` 切断连接。
8. `frontend/.npmrc` 里的 `minimum-release-age` 只对 pnpm/bun 生效，**npm 会忽略它**，
   所以新增依赖不受那个 7 天供应链保护约束。

## 设计取舍

- 路由用标准库 `net/http` 的 `METHOD /path` 模式：9 条静态路由用不上 router。
- 不引 `air`：Go 侧就几个小文件，为一个 2 秒动作加全局工具依赖不划算。
- Naive UI 只承担「行为不想手写」的部分（浮层、焦点陷阱、树、弹层、确认框）；
  按钮、分段控件、输入框外观仍是自己的 CSS——组件库主题只收平面色，换过去会丢掉
  现有质感。
