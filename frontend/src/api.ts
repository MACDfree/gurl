import type {
  CollectionNode,
  EnvState,
  HTTPRequest,
  HTTPResponse,
  HistoryEntry,
} from "./types";

// 生产环境同源（Go 直接托管 dist）；开发时由 Vite 代理转发 /api。
const API = "";

/**
 * 会话 token。
 *
 * Go 启动时用 http://127.0.0.1:PORT/?t=… 打开浏览器，这里收下、存进 sessionStorage，
 * 再把地址栏抹干净（否则 token 会留在地址栏、浏览历史、截图和别人转发的链接里）。
 *
 * 用 sessionStorage 而不是模块变量：浏览器模式下 ⌘R 是一等操作，重载后还得拿得到。
 * 不用 localStorage：token 绑定的是那个具体进程，不该活过这个标签页。
 * 开发模式下这里是空串——token 由 Vite 代理注入，浏览器根本不需要知道。
 */
function readToken(): string {
  const fromUrl = new URLSearchParams(location.search).get("t");
  if (fromUrl) {
    sessionStorage.setItem("gurl.token", fromUrl);
    history.replaceState(null, "", location.pathname);
  }
  return sessionStorage.getItem("gurl.token") ?? "";
}

const token = readToken();

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

async function rpc<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = {};
  if (token) headers.authorization = `Bearer ${token}`;
  if (body !== undefined) headers["content-type"] = "application/json";

  let res: Response;
  try {
    res = await fetch(API + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal });
  } catch (e) {
    // 后端被杀掉时 fetch 直接 reject。必须转成统一错误类型，否则调用方的 finally
    // 不跑、按钮永久停在「发送中」。
    if (e instanceof DOMException && e.name === "AbortError") throw new ApiError("请求已中断", 0);
    throw new ApiError("连不上本地后端，确认 gurl 还在运行", 0);
  }

  // 一律先读成文本：代理/静态层偶尔会回一个非 JSON 的错误页，直接 res.json() 会抛
  // 一个和后端错误毫无关系的 SyntaxError。
  const text = await res.text();
  let env: Envelope<T> | null = null;
  try {
    env = JSON.parse(text) as Envelope<T>;
  } catch {
    /* 落到下面的 HTTP 状态兜底 */
  }

  if (!res.ok || env === null || env.ok === false) {
    throw new ApiError(env?.error?.trim() || `HTTP ${res.status}`, res.status);
  }
  return env.data;
}

// 以下函数名与旧的 Wails bindings 保持一致：迁移只改 import 路径，调用点零改动。

export function Send(req: HTTPRequest) {
  // 客户端兜底超时：Go 侧 timeoutMs 才是权威（用户看到它的中文报错），
  // 这里只保证后端被强杀时页面不会永远转圈。
  const budget = (req.timeoutMs > 0 ? req.timeoutMs : 30000) + 10000;
  return rpc<HTTPResponse | null>("POST", "/api/send", req, AbortSignal.timeout(budget));
}

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

export function GetCollections() {
  return rpc<(CollectionNode | null)[] | null>("GET", "/api/collections");
}

export function SaveCollections(nodes: CollectionNode[]) {
  return rpc<void>("PUT", "/api/collections", nodes);
}
