import type { FileKV, HTTPRequest, HTTPResponse, KV, SSEEvent } from "./types";
import { uuid } from "./util";

export type ReqTab = "params" | "headers" | "body";
export type ResTab = "body" | "headers" | "stream";
export type BodyType = "none" | "json" | "raw" | "form" | "multipart";

/** 一张标签页的请求编辑态；字段与 HTTPRequest 对齐，序列化成快照时不用转换。 */
export interface ReqState {
  method: string;
  url: string;
  params: KV[];
  headers: KV[];
  bodyType: BodyType;
  body: string;
  form: KV[];
  files: FileKV[];
  bodyGzip: boolean;
  timeoutMs: number;
  reqTab: ReqTab;
}

/**
 * 上一次请求的结果。挂在标签页上而不是全局，后台还在跑的流才只会写自己那一份——
 * 否则你在 A 页发起 LLM 流式请求、切到 B 页看别的，事件会一股脑灌进 B 的面板。
 */
export interface ResState {
  sending: boolean;
  resp: HTTPResponse | null;
  errorMsg: string;
  respTab: ResTab;
  isStream: boolean;
  events: SSEEvent[];
  /** 尾部窗口起点：只渲染最近 MAX_SHOWN_EVENTS 条 */
  shownFrom: number;
  eventTotal: number;
}

export interface Tab {
  id: string;
  /** 绑定的集合节点；空串表示还没存过 */
  nodeId: string;
  req: ReqState;
  res: ResState;
  /** 载入/保存那一刻的请求快照，用来标「有未保存改动」——比 deep watch 便宜 */
  saved: string;
}

export function emptyReq(): ReqState {
  return {
    method: "GET",
    url: "",
    params: [],
    headers: [],
    bodyType: "raw",
    body: "",
    form: [],
    files: [],
    bodyGzip: false,
    timeoutMs: 30000,
    reqTab: "params",
  };
}

export function emptyRes(): ResState {
  return {
    sending: false,
    resp: null,
    errorMsg: "",
    respTab: "body",
    isStream: false,
    events: [],
    shownFrom: 0,
    eventTotal: 0,
  };
}

export function newTab(): Tab {
  const req = emptyReq();
  return { id: uuid(), nodeId: "", req, res: emptyRes(), saved: JSON.stringify(requestOf(req)) };
}

/** 从一份请求（集合节点、历史条目、cURL 导入结果）填出编辑态；缺的字段回落到默认值。 */
export function reqFrom(r: Partial<HTTPRequest> | null | undefined): ReqState {
  const d = emptyReq();
  if (!r) return d;
  const bodyType = (r.bodyType || "raw") as BodyType;
  return {
    method: r.method || "GET",
    url: r.url || "",
    params: (r.params ?? []).map((k) => ({ ...k })),
    headers: (r.headers ?? []).map((k) => ({ ...k })),
    bodyType,
    body: r.body ?? "",
    form: (r.form ?? []).map((k) => ({ ...k })),
    files: (r.files ?? []).map((f) => ({ ...f })),
    bodyGzip: !!r.bodyGzip,
    timeoutMs: r.timeoutMs || 30000,
    reqTab: bodyType === "none" ? "params" : "body",
  };
}

/** 编辑态 → 发出去的请求。gzip 只对 json/raw 有意义，这条规则原来散在组件里，收到一处。 */
export function requestOf(req: ReqState): HTTPRequest {
  const gzipable = req.bodyType === "json" || req.bodyType === "raw";
  return {
    method: req.method,
    url: req.url,
    params: JSON.parse(JSON.stringify(req.params)),
    headers: JSON.parse(JSON.stringify(req.headers)),
    bodyType: req.bodyType,
    body: req.body,
    form: JSON.parse(JSON.stringify(req.form)),
    files: req.files.filter((f) => f.key && f.path).map((f) => ({ ...f })),
    bodyGzip: gzipable && req.bodyGzip,
    timeoutMs: req.timeoutMs,
  };
}

export function snapshot(req: ReqState): string {
  return JSON.stringify(requestOf(req));
}

/** 标签页标题：优先用集合里的名字，否则 URL 末段，最后「未命名」。 */
export function tabTitle(t: Tab): string {
  const u = t.req.url.trim();
  if (!u) return t.nodeId ? "（已存）" : "未命名";
  const path = u.replace(/[?#].*$/, "").replace(/\/+$/, "");
  return path.split("/").pop() || u;
}
