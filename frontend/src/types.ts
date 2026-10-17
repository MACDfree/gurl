import type { Ref } from "vue";

// ---- 线格式类型：与 Go 侧 struct tag 一一对应 ----
// 唯一的定义处。Go 侧改了 tag 必须同步这里，server_test.go 的 TestAPIWireContract
// 会在 Go 测试里报出 key 集合变化。

export interface KV {
  key: string;
  value: string;
  enabled: boolean;
}

export interface FileKV {
  key: string;
  /** 后端所在机器上的绝对路径，由原生文件对话框返回 */
  path: string;
}

export type BodyType = "none" | "json" | "raw" | "form" | "multipart";

export interface HTTPRequest {
  method: string;
  url: string;
  params: KV[] | null;
  headers: KV[] | null;
  /** 空串按 raw 处理 */
  bodyType: BodyType | "";
  body: string;
  form: KV[] | null;
  files: FileKV[] | null;
  bodyGzip: boolean;
  timeoutMs: number;
  /** 从这次响应里提取值存成运行时变量的规则，见 tabs.ts 与后端 extract.go */
  extract?: ExtractRule[] | null;
  /** 认证预设；后端在注入时不覆盖你手写的同名头 */
  auth?: Auth | null;
}

/** type 为 none 表示没用预设；apikey 的 in 决定它落在头还是查询参数上。 */
export interface Auth {
  type: "none" | "basic" | "bearer" | "apikey";
  user?: string;
  pass?: string;
  token?: string;
  key?: string;
  value?: string;
  in?: "header" | "query";
}

/** source 为空按 body 处理；path 在 body 下是 data.token / items[0].id，在 header 下是头名。 */
export interface ExtractRule {
  name: string;
  source?: "body" | "header" | "";
  path: string;
}

export interface HTTPResponse {
  status: number;
  statusText: string;
  headers: KV[] | null;
  body: string;
  isText: boolean;
  truncated: boolean;
  /** 非空 = 完整正文已落在后端缓存，可用 /api/response/{id} 取回 */
  bodyId?: string;
  /** 本次按规则取到的运行时变量；没取到的名字不出现，拿规则一比就知道哪条没命中 */
  extracted?: Record<string, string> | null;
  durationMs: number;
  size: number;
  timings?: Timings;
}

/**
 * 一次请求各阶段的耗时（毫秒）。缺省/0 = 该阶段没发生：复用连接就没有 DNS、连接、TLS，
 * 直连 IP 不解析，没跳转就没有 hops。ttfbMs 已经包含前面的 DNS/连接/TLS。
 */
export interface Timings {
  dnsMs?: number;
  connectMs?: number;
  tlsMs?: number;
  ttfbMs?: number;
  bodyMs?: number;
  reused?: boolean;
  hops?: number;
}

export interface HistoryEntry {
  /** RFC3339 */
  time: string;
  request: HTTPRequest;
  status: number;
  durationMs: number;
  size: number;
  error: string;
}

/** 一条 Server-Sent Event；多个 data: 行已由后端拼成一个 data。 */
export interface SSEEvent {
  id: string;
  /** 上游没写 event: 时后端补 "message" */
  type: string;
  data: string;
}

/** 流式响应的状态行 + 表头，正文不在这里——正文是随后的一条条事件。 */
export interface StreamMeta {
  status: number;
  statusText: string;
  headers: KV[] | null;
}

export interface Environment {
  name: string;
  vars: KV[] | null;
}

export interface EnvState {
  active: string;
  envs: Environment[] | null;
  /** 提取出来的运行时变量：只在后端内存里，重启即失效。写方向缺席 = 别动它。 */
  runtime?: KV[] | null;
}

/** 一条 cookie 记录（后端 cookie 罐；无 requests 载荷，走独立的 /api/cookies）。 */
export interface CookieRecord {
  name: string;
  value: string;
  domain: string;
  /** 捕获时 Set-Cookie 未带 Domain：只对精确主机生效；false 表示对子域也生效 */
  hostOnly: boolean;
  /** 解析后总以 / 开头；不进 UI，随记录原样保存 */
  path: string;
  /** 不进 UI，随记录原样保存 */
  secure: boolean;
  /** Unix 秒；0 = 会话 cookie */
  expires: number;
  enabled: boolean;
}

export interface CollectionNode {
  id: string;
  name: string;
  /** collection | folder | request */
  type: string;
  children?: (CollectionNode | null)[] | null;
  request?: HTTPRequest | null;
}
