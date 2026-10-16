# gurl

一个跑在浏览器里的 API 调试工具：单二进制起本地服务，界面在浏览器标签页里。
支持 Params / Headers / Body（JSON、raw、urlencoded、form-data 含文件上传）、请求体 gzip、
响应解压与预览、SSE 流式响应（逐事件显示、可中途停止）、历史记录、环境变量 `{{var}}`、
集合树（可拖拽排序）。

## 跑起来

```bash
# 构建：前端产物 → frontend/dist，embed 进单二进制 → bin/gurl
cd frontend && npm install && npm run build && cd ..
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w" -o bin/gurl .
./bin/gurl          # 起服务并自动打开浏览器

# 开发模式：后端与 Vite 各占一个终端
GURL_DATA_DIR=bin go run . -dev -open=false -port=9246    # 终端 1：后端
cd frontend && npm run dev                                # 终端 2：Vite（9245，/api 代理到 9246）

go test ./... -race   # 带竞态检测跑测试
go vet ./...          # 静态检查
go mod tidy           # 整理依赖
```

开发时打开 `http://127.0.0.1:9245/`。前端改动即时热更新；**Go 改动需要手动重启后端**。

## 结构

```
main.go            监听 :0（内核挑端口）、开浏览器、优雅退出
server.go          中间件栈 + 路由；handlers.go  10 个端点（9 个 JSON + 1 个 NDJSON 流）
guard.go           Host/Origin 校验：只让本机与 -allow-origin 白名单调 /api（挡跨源页面与 DNS rebinding）
static.go          embed 的前端产物 + 缓存头 + 窄化的 SPA 兜底
requestservice.go  发请求、解压、双上限预览
stream.go          text/event-stream 判定与 SSE 分帧，逐帧推 NDJSON
bodycache.go       超限响应落盘缓存（供「下载完整响应」）
history/env/collections.go   配置持久化（可执行文件同级的 *.json，GURL_DATA_DIR 可覆盖）
frontend/          Vue 3 + TS + Vite + Naive UI + CodeMirror 6
```

API 一律返回 `{"ok":true,"data":…}` / `{"ok":false,"error":"中文原因"}`；
服务层错误走 HTTP 500，前端因此只有一条失败分支，不存在「静默失败」。

## 只服务本机页面

服务只绑 127.0.0.1，但本机任意进程、任意网页都能打到回环；没有检查时，访问一个恶意
网页就足以把 gurl 变成「可发任意 HTTP 请求、还会写你配置文件」的中继。所以 `/api/*`
全部过一层 `guard.go`：Host 校验挡 DNS rebinding（evil.com 解析到 127.0.0.1 时 Host
就不是回环名了），Origin 校验挡跨源页面（放行任意端口，因为 dev 下浏览器的 Origin
是 Vite 端口而请求落在 Go 端口）。

前后端分机部署时（反向代理出去，或用另一台机器的浏览器直接打开后端地址），浏览器带的
Host/Origin 就不再是回环名，用 `-allow-origin` 显式放行：

```bash
./bin/gurl -allow-origin gurl.example.com           # 反代：填浏览器实际访问的域名
./bin/gurl -host 0.0.0.0 -allow-origin 192.168.1.5  # 直开：填后端所在的局域网 IP
```

条目按主机名匹配，端口、协议、大小写都不参与（域名 / IP / 完整 URL 都能写）；所列主机名
同时被 Host 与 Origin 两层接受，所以 nginx 保不保留 Host 都能用。这个 flag 就是唯一开口，
不填时行为与以前完全一致。两条部署提醒：gurl 没有任何鉴权，白名单不是权限系统，别把实例
暴露给不受信网络；Nginx 反代 SSE 要关 `proxy_buffering`，默认缓冲会把逐事件推送攒着不发。

不加任何 CORS：跨源页面只能免预检发「简单请求」，而这里写端点要求 JSON 体、
PUT/DELETE——任意一条都强制预检，零 CORS 头即失败关闭。**因此「写端点必须收 JSON
体」是 CSRF 防线，不是代码风格。**

## 保存与更新

- 工具栏「保存」或 ⌘S：新请求弹出小浮层（名字默认取 URL 末段、位置默认顶层），保存后该节点即成为"当前请求"。
- 从集合里打开的请求按 ⌘S → **直接就地覆盖那个节点**，不弹窗、右上角 toast 确认。
- 更新不改位置；要把节点挪到别的集合/目录，在树里拖拽。

## 流式（SSE）

上游响应头是 `Content-Type: text/event-stream` 时自动进入流式，不需要开关——判定看响应，
所以不用先猜对上游会不会流式。`POST /api/stream` 按行回 NDJSON（`meta` → N×`event` →
`end`；非流式上游则整条走老路，回一帧 `response`），前端 `fetch` + `ReadableStream`
逐帧渲染成事件列表，状态码和表头在第一个事件到达之前就已经显示。

- **`timeoutMs` 在流式下只约束「拿到响应头」**，正文可以一直挂着（SSE 本来就不结束）。
  想中断点「停止」——发送中的时候那个按钮就是停止。
- 上游要 `Accept: text/event-stream` 才肯流式的话，自己在 Headers 页加上；gurl 不替你
  注入 Accept，那会改变所有普通请求的协商结果。
- 事件流同样受 `hardCap`（64 MiB）约束，读满就停并标记截断。
- 面板只渲染最近 500 条（一次 LLM 回答动辄几千个 event，全塞进 DOM 会把页面钉死），
  上面提示省略了多少条。

## 已知限制

1. **文件上传读的是后端所在机器的文件**。文件选择框在服务端弹窗、返回服务端绝对路径；
   也可以直接在路径框手输服务端绝对路径（跨机访问、后端无 GUI 时走这条）。
   前后端不同机时，上传浏览器本机文件做不到。
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
9. **流式请求在正文阶段没有总时限**：`timeoutMs` 只管拿到响应头，之后一条 SSE 挂多久
   算多久（这就是这个功能的本意）。能中止它的只有 64 MiB 上限、上游关连接、点「停止」
   或关掉标签页；一条长连接会一直占着浏览器 6 个同源连接里的一个。

## 设计取舍

- 路由用标准库 `net/http` 的 `METHOD /path` 模式：10 条静态路由用不上 router。
- 不引 `air`：Go 侧就几个小文件，为一个 2 秒动作加全局工具依赖不划算。
- 不留 Makefile / Taskfile：构建、开发、测试就是「跑起来」里那几条命令，还没多到
  要一个专门的构建入口来记。
- Naive UI 只承担「行为不想手写」的部分（浮层、焦点陷阱、树、弹层、确认框）；
  按钮、分段控件、输入框外观仍是自己的 CSS——组件库主题只收平面色，换过去会丢掉
  现有质感。
