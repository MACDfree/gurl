import type {
  CollectionNode,
  CookieRecord,
  EnvState,
  HTTPRequest,
  HTTPResponse,
  HistoryEntry,
  SSEEvent,
  StreamMeta,
} from "./types";

// 生产环境同源（Go 直接托管 dist）；开发时由 Vite 代理转发 /api。
const API = "";

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

interface Envelope<T> {
  ok: boolean;
  data: T;
  error?: string;
}

/** 只负责发出去：两种读取方式（一次读完 / 逐帧读）共用同一段取数与错误处理。 */
async function open(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<Response> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["content-type"] = "application/json";
  try {
    return await fetch(API + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch (e) {
    // 后端被杀掉时 fetch 直接 reject。必须转成统一错误类型，否则调用方的 finally
    // 不跑、按钮永久停在「发送中」。
    if (e instanceof DOMException && e.name === "AbortError") throw new ApiError("请求已中断", 0);
    throw new ApiError("连不上本地后端，确认 gurl 还在运行", 0);
  }
}

// 一律先读成文本再判：代理/静态层偶尔会回一个非 JSON 的错误页，直接 res.json() 会抛
// 一个和后端错误毫无关系的 SyntaxError。
function apiError(status: number, text: string): ApiError {
  let env: Envelope<unknown> | null = null;
  try {
    env = JSON.parse(text) as Envelope<unknown>;
  } catch {
    /* 落到 HTTP 状态兜底 */
  }
  return new ApiError(env?.error?.trim() || `HTTP ${status}`, status);
}

async function rpc<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const res = await open(method, path, body, signal);
  const text = await res.text();
  let env: Envelope<T> | null = null;
  try {
    env = JSON.parse(text) as Envelope<T>;
  } catch {
    /* 交给 apiError 一起处理 */
  }
  if (!res.ok || env === null || env.ok === false) throw apiError(res.status, text);
  return env.data;
}

/** /api/stream 的一帧。后端按行写 NDJSON，一行一帧。 */
type StreamFrame =
  | { type: "meta"; meta: StreamMeta }
  | { type: "event"; event: SSEEvent }
  | { type: "end"; durationMs?: number; size?: number; truncated?: boolean }
  | { type: "response"; response: HTTPResponse }
  | { type: "error"; error?: string };

export interface StreamSink {
  /** 上游不是 event-stream：整条走全缓冲，一帧给完 */
  response?: (r: HTTPResponse) => void;
  meta?: (m: StreamMeta) => void;
  event?: (e: SSEEvent) => void;
  end?: (info: { durationMs: number; size: number; truncated: boolean }) => void;
  /** 流已经在跑了，出错时不能再用状态码表达，只能补一帧 */
  fail?: (msg: string) => void;
}

/**
 * POST /api/stream：一次请求，按帧回调。
 *
 * 为什么不用 EventSource：它只能 GET，而这里的请求要带 body（POST 调 AI 接口正是主要
 * 场景）。fetch + ReadableStream 是唯一能流式读响应体的浏览器 API。
 *
 * 这条路上没有客户端超时：后端 timeoutMs 只约束「拿到响应头」，正文本来就该一直挂着，
 * 想中断有「停止」。后端被强杀时 read() 会 reject，落到下面的统一错误里。
 */
export async function SendStream(req: HTTPRequest, sink: StreamSink, signal?: AbortSignal): Promise<void> {
  const res = await open("POST", "/api/stream", req, signal);
  if (!res.ok) throw apiError(res.status, await res.text());
  if (!res.body) throw new ApiError("后端没有返回流", res.status);

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  const feed = (line: string) => {
    if (!line) return;
    applyFrame(JSON.parse(line) as StreamFrame, sink); // 坏行说明前后端对不上，直接抛出来
  };
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      // { stream: true }：一个多字节字符正好被 chunk 切开时不能吐 U+FFFD。
      // 帧边界和 UTF-8 边界没有任何关系，中文流式响应全靠这个参数。
      buf += decoder.decode(value, { stream: true });
      let i: number;
      while ((i = buf.indexOf("\n")) >= 0) {
        feed(buf.slice(0, i));
        buf = buf.slice(i + 1);
      }
    }
    if (buf.trim()) feed(buf);
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw new ApiError("请求已中断", 0);
    throw e;
  }
}

function applyFrame(f: StreamFrame, sink: StreamSink) {
  switch (f.type) {
    case "meta":
      sink.meta?.(f.meta);
      break;
    case "event":
      sink.event?.(f.event);
      break;
    case "end":
      sink.end?.({ durationMs: f.durationMs ?? 0, size: f.size ?? 0, truncated: !!f.truncated });
      break;
    case "response":
      sink.response?.(f.response);
      break;
    case "error":
      sink.fail?.(f.error || "流式请求失败");
      break;
  }
}

// 以下函数名与旧的 Wails bindings 保持一致：迁移只改 import 路径，调用点零改动。
// Send 是唯一的例外——它换成了 SendStream，非流式请求由后端回一帧 response 走老路。

export function PickFile() {
  return rpc<string>("POST", "/api/pick-file");
}

export function GetHistory() {
  return rpc<HistoryEntry[] | null>("GET", "/api/history");
}

export function ClearHistory() {
  return rpc<void>("DELETE", "/api/history");
}

export function GetEnvState() {
  return rpc<EnvState>("GET", "/api/env");
}

export function SaveEnvState(state: EnvState) {
  return rpc<void>("PUT", "/api/env", state);
}

export function GetCookies() {
  return rpc<CookieRecord[] | null>("GET", "/api/cookies");
}

export function SaveCookies(records: CookieRecord[]) {
  return rpc<void>("PUT", "/api/cookies", records);
}

export function GetCollections() {
  return rpc<(CollectionNode | null)[] | null>("GET", "/api/collections");
}

export function SaveCollections(nodes: CollectionNode[]) {
  return rpc<void>("PUT", "/api/collections", nodes);
}

/**
 * 完整响应的下载地址。
 *
 * 浏览器发起的下载（<a href>）直接交给后端流式返回，不走 fetch+Blob——后者会把上限
 * 那么大的字节拉进 JS 堆。
 */
export function responseDownloadUrl(id: string): string {
  return `/api/response/${encodeURIComponent(id)}`;
}
