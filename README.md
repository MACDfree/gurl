# gurl

一个跑在浏览器里的 API 调试工具：单二进制起本地服务，界面在浏览器标签页里。
支持 Params / Headers / Body（JSON、raw、urlencoded、form-data 含文件上传）、请求体 gzip、
多标签页编辑、从响应提取变量串请求链、集合与环境的导入导出、响应解压与预览（图片/音频/视频直接渲染）、SSE 流式响应（逐事件显示、可中途停止）、历史记录、环境变量 `{{var}}`（写错当场提示）、
Cookie 罐（自动捕获/携带，模拟登录态）、集合树（可拖拽排序）、cURL 导入导出、侧栏筛选、
各阶段耗时分解。

[![license](https://img.shields.io/badge/license-Apache--2.0-green?style=flat-square)](https://github.com/MACDfree/gurl/blob/master/LICENSE)
[![release](https://img.shields.io/github/v/release/MACDfree/gurl?style=flat-square&color=green)](https://github.com/MACDfree/gurl/releases)
[![downloads](https://img.shields.io/github/downloads/MACDfree/gurl/total?style=flat-square&color=blue)](https://github.com/MACDfree/gurl/releases)
[![stars](https://img.shields.io/github/stars/MACDfree/gurl?style=flat-square&color=yellow)](https://github.com/MACDfree/gurl/stargazers)
[![Go](https://img.shields.io/github/go-mod/go-version/MACDfree/gurl?style=flat-square&logo=go&logoColor=white&color=00ADD8)](https://go.dev)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-blue?style=flat-square)](https://github.com/MACDfree/gurl/releases)

## 下载

不想自己编译：从 [Releases](https://github.com/MACDfree/gurl/releases) 下载对应平台的压缩包
（macOS / Linux / Windows），解压后运行里面的 `gurl`（Windows 为 `gurl.exe`）。

macOS 上需要注意：Release 里的二进制未经 Apple 签名与公证，从浏览器下载后首次运行会被
Gatekeeper 拦下（「无法验证开发者」）。这是签名检查，不是病毒检测，解压后去掉隔离标记即可：

```bash
xattr -d com.apple.quarantine ./gurl
```

也可以在 Finder 里按住 Control 点击 →「打开」→「打开」。改用 `curl -L` 从终端下载则不会
打上隔离标记，解压后可直接运行。

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
server.go          中间件栈 + 路由；handlers.go  13 个端点（12 个 JSON + 1 个 NDJSON 流）
guard.go           Host/Origin 校验：只让本机与 -allow-origin 白名单调 /api（挡跨源页面与 DNS rebinding）
static.go          embed 的前端产物 + 缓存头 + 窄化的 SPA 兜底
requestservice.go  发请求、解压、双上限预览
extract.go         按规则从响应取值守成运行时变量（data.token / 表头），替换时盖过同名环境变量
timings.go         httptrace 采集各阶段耗时（DNS/连接/TLS/首字节/正文/跳转数）
stream.go          text/event-stream 判定与 SSE 分帧，逐帧推 NDJSON
bodycache.go       超限响应落盘缓存（供「下载完整响应」）
history/env/collections/cookies.go   配置持久化（可执行文件同级的 *.json，GURL_DATA_DIR 可覆盖）
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

## 标签页

请求面板上面那条药丸就是标签页：一张页一份请求 + 一份响应，切换不会覆盖没保存的东西。
只在需要时才出现（多于一个标签页，或者当前页有未保存改动），＋ 新建，× 关闭。
点集合节点 / 历史 / cURL 导入都是装进**当前**这一页，不是另开一页。

- 点状标记：灰色=干净，琥珀=有未保存改动，蓝色闪动=这一页的请求还在跑。
- **后台的流只写自己那一页**：在 A 页发起 SSE 流式请求、切到 B 页干别的，事件会攒在 A 的
  `Run` 里（引擎状态放在组件外的 Map 中，不进响应式），切回 A 才看到完整序列；A 的节流
  定时器与 AbortController 也都是每页一份。关掉一页等于放弃它那条在跑的流。
- 状态住在 `tabs.ts` 的 `Tab` 上，界面上那些 `url` / `resp` / `events` 只是指向
  「当前标签页那个字段」的可写 computed——所以模板和原有逻辑几乎没改，而
  「切换 = 覆盖一份全局状态」这个模型已经不成立了。

## 侧栏筛选

历史与集合两个面板共用一个筛选框：⌘F 聚焦（面板关着会顺手打开），Esc 或框里的 × 清空。
按整段文本匹配、不区分大小写——历史看方法、URL、状态码和错误原因；集合看节点名**和请求的
URL 与方法**，所以「按 URL 找请求」不必先记得它挂在哪个目录。筛选时命中节点的祖先目录自动
展开（不然搜到了却看不见），同时暂时关掉拖拽排序：只剩一条分支的树里，拖拽落点没有意义。

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

## Cookie 罐

模拟已登录请求：登录请求（含 302 跳转链）每一跳的 Set-Cookie 都会被自动捕获——中间跳的
Set-Cookie 在响应面板里看不到，罐里记得住——存进可执行文件同级的 `cookies.json`，之后发往
匹配域名的请求自动携带，重启不丢。顶栏饼干图标打开管理弹窗：增删改、启用/停用、清空。

- **手动 Cookie 头优先**：Headers 里手填了 Cookie 头时，该请求完全不挂罐——否则
  `http.Client` 会把罐里的 cookie 拼在手写值后面，同名两份。这也是「某次请求不想要罐」的开关。
- 停用的 cookie 不再携带；同槽位再次被捕获时只刷新值，停用状态保留。
- 域名列前加 `.` 表示对子域生效（浏览器 DevTools 同款写法）。匹配规则（host-only /
  路径 / Secure / 过期）交给 stdlib 的 cookiejar；没有 publicsuffix 校验，`foo.co.uk`
  能被 `co.uk` 设 cookie，本地调试工具接受这一宽松语义。

## cURL 导入 / 导出

顶栏 `</>` 按钮，两个页签。纯前端功能（`frontend/src/curl.ts`），不经过后端。

- **导入**直接吃浏览器 DevTools 的「Copy as cURL」：多行续行、`#` 注释、单双引号与
  `'\''` 转义、`-sSL` 这类短选项连写、`-XPOST` 这类值紧贴写法都认。URL 自带的 query 拆进
  Params；`-d 'a=1&b=2'` 拆成 urlencoded 表单；`-F k=@/path` 进文件行；`-u`/`--oauth2-bearer`
  变成 Authorization 头；`-b 'a=1; b=2'` 变成 Cookie 头（按 gurl 的规则，手写 Cookie 头会让
  这次请求不挂 Cookie 罐）。
- **不静默丢东西**：gurl 没有对应能力的选项（`-k`、`--proxy`、`-o`、`--data @文件` 等）在
  弹窗底部逐条列出，导入前就能看见。
- **导出**给的是能直接贴进终端的等价命令：`-L` 与 `--compressed` 对应 gurl 的默认行为
  （跟随跳转、自动解压），json 类型补上 gurl 代发的 `Content-Type`。form-data 的 boundary
  不导出（每次发送都不同）。请求体勾了 gzip 时，curl 没有对应选项，导出的是未压缩原文并
  在命令上方注明。
- 导出默认把 `{{var}}` 换成当前环境的值（复选框可关，切回原样）；文件路径是**后端所在机器**
  的路径，跨机时照已知限制 1 处理。

## 耗时分解

响应卡片头部下面那条细瀑布来自 `net/http/httptrace`：**DNS → 连接 → TLS → 等首字节 → 正文**。
分段宽度按真实占比，0 的阶段不画（复用连接就没有 DNS/连接/TLS，直连 IP 不做解析）。「等首字节」
是首字节时间减去前三段——`ttfbMs` 本身已经把前面包进去了，不减会重复计账。

- 30x 跳转链上的 DNS/连接/TLS **累加**统计，旁边标「跟随 N 次跳转」：跳到另一个域名等于重来
  一遍，只报最后一跳会偏小。
- 发生过但不足 1ms 的按 1ms 报，好让「0」始终意味着「这一步没做」。
- 流式请求在 `meta` 帧就给一份（那时首字节已经到手，正是这个场景最想看的数），`end` 帧再补全
  正文时间；对 SSE 而言正文时间就是事件流持续了多久。
- 复用了连接会标出来。走的是 `http.Client` 默认连接池，所以同一个上游连发两次，第二次通常只剩
  等首字节和正文两段。

## 提取变量（请求链）

「先登录拿 token，再带着它调别的接口」不必手工复制粘贴：请求面板的**提取**页签里给一条规则
（变量名 + 来源 + 路径），下一次响应的值就存成运行时变量，之后的 `{{token}}` 直接可用。

    POST /login  →  规则 token ← 正文 data.token  →  GET /me 带 Authorization: Bearer {{token}}

- 路径是 `data.token` / `items[0].id` 这种点加下标（`$.` 前缀也认），取到对象/数组就给紧凑
  JSON；来源换成「表头」则按头名取（不区分大小写）。取不到的规则不报错，只是不出现在
  `extracted` 里——前端拿规则一比就知道哪条没命中。
- **运行时变量不写进 env.json**。环境里是用户手填的值，让每次响应去覆盖它，一旦路径写错或
  上游改了结构，就会把人肉配置悄悄改坏；所以它只在后端内存里，替换时盖在同名环境变量上面，
  重启即失效。提取页签下面看得见当前值，也能一键清空。
- `PUT /api/env` 里 `runtime` 缺席 = 别动它，显式给（哪怕是空表）= 整体替换。界面每次改环境
  都会整体 PUT 一次，要是把「缺席」当成「清空」，改个环境名就把运行时变量抹了。
- 流式（SSE）请求没有「完整正文」，所以只有表头类规则能命中。

## 未定义变量提示

`{{var}}` 拼错时后端不会报错——`subst()` 认不出来就原样发出去，于是 `{{baseURl}}` 变成
一个字面量进了 URL，你得等到看不懂的上游 400 才想起来是这儿打错了。所以请求里任何一处
（URL、Params、Headers、Body、表单、文件路径）出现当前环境没有的变量，工具栏下面就会出
一条琥珀色提示，列出这些名字。

名字旁边给一个编辑距离最近的建议（差超过 2 就不瞎猜了）：点一下把**整个请求里**所有
`{{baseURl}}` 换成 `{{baseURL}}`，不是只改你打错的那一处。右侧「改环境」直达环境管理。

匹配是字面的，和后端一致：`{{ token }}` 里的空格算名字的一部分，照样会提示——因为后端
确实不会替换它。不引 CodeMirror 的补全包（`@codemirror/autocomplete` 是新的直接依赖，
而 `frontend/.npmrc` 那个 `minimum-release-age` 对 npm 无效），提示 + 一键改已经覆盖了
「打错」和「记不住名字」两件事。

## 二进制预览

图片、音频、视频响应直接在响应面板里渲染（棋盘格垫底，透明图看得出边界），下面照旧给
「下载完整响应」。落盘走的是超限响应那套缓存，所以预览和下载是同一份字节——不会是
「屏幕上看的是一张、下下来是另一张」。

`GET /api/preview/{id}` 只给白名单里的类型 inline，其余一律退回下载：
**HTML、SVG、PDF 都不在名单上**。这三个都能带着本机 origin 执行脚本，其中 PDF 最隐蔽——
浏览器内嵌查看器会跑文档里的 JS，那个脚本的 origin 就是 gurl 自己，等于把 `/api` 整套
交出去。SVG 也不亏，它是合法文本，本来就走代码预览。认出来的类型外面还压一层
`Content-Security-Policy: default-src 'none'; sandbox` + `nosniff`，万一判断出错也跑不了脚本。

## 备份（集合 + 环境）

顶栏两个箭头图标：导出把**集合与环境合成一个 JSON** 下载到本地；导入认这个文件，也认
`collections.json`（裸集合数组）和 `env.json`（裸环境状态）。合成一个文件是因为这俩本来是
一套配置——集合里的请求满是 `{{baseURL}}`，只备份集合等于备份一堆跑不起来的东西。

- 导入先出摘要（几个顶层集合、几个请求、几个环境、文件里的当前环境是谁），再让你选
  **追加**还是**替换**。追加保留现有内容，节点拿新 id 挂在顶层，环境撞名就把进来的改名；
  替换是整体覆盖，按钮要按第二次才生效——一次误触就把别人攒了几个月的配置换掉，这种事不该
  由一次点击决定。
- 解析宽容但不静默：缺 `type` 的按内容归类、空节点丢掉、文件内重名环境改名，全写在摘要下面
  的警告里。认不出来的文件直接说「没认出可导入的内容」，不猜。
- 两种导入都不碰运行时变量（那是上一次响应提取出来的值，重启本来就没）。
- 备份文件里是**明文**变量值（token 之类），与 `env.json` 同级；下载到下载目录后自己留意。

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
10. **Cookie 罐是全实例共享的全局状态**：任何请求捕获的 Set-Cookie 都会自动存盘并携带，
    多标签页共用同一份（与第 2 条的跨标签页限制同类）；cookie 值明文落盘，与集合、
    环境变量同级。不想让某次请求碰罐，在手写 Cookie 头即可。

## 设计取舍

- 路由用标准库 `net/http` 的 `METHOD /path` 模式：12 条静态路由用不上 router。
- 不引 `air`：Go 侧就几个小文件，为一个 2 秒动作加全局工具依赖不划算。
- 不留 Makefile / Taskfile：构建、开发、测试就是「跑起来」里那几条命令，还没多到
  要一个专门的构建入口来记。
- Naive UI 只承担「行为不想手写」的部分（浮层、焦点陷阱、树、弹层、确认框）；
  按钮、分段控件、输入框外观仍是自己的 CSS——组件库主题只收平面色，换过去会丢掉
  现有质感。
