import type { Auth, ExtractRule, FileKV, Options, HTTPRequest, HTTPResponse, KV, SSEEvent } from "./types";
import { uuid } from "./util";

export type ReqTab = "params" | "headers" | "body" | "extract" | "auth";
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
  extract: ExtractRule[];
  auth: Auth;
  opts: Options;
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
  /** 自定义标签名；空则回落到集合节点名 / URL 末段 */
  title?: string;
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
    extract: [],
    auth: { type: "none" },
    opts: {},
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

/**
 * 干净空页。标签条藏不藏的判据不能只看「有没有未保存改动」——从历史/集合载入的请求
 * 不算改动，但页面已经不是空的；这时再藏，＋ 就永远点不到了。
 */
export function blankTab(t: Tab): boolean {
  return (
    !t.title &&
    !t.nodeId &&
    !t.res.resp &&
    !t.res.errorMsg &&
    !t.res.sending &&
    snapshot(t.req) === snapshot(emptyReq())
  );
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
    extract: (r.extract ?? []).map((e) => ({ ...e })),
    auth: { type: "none", in: "header", ...(r.auth ?? {}) },
    opts: { ...(r.opts ?? {}) },
    reqTab: bodyType === "none" ? "params" : "body",
  };
}

/** 一个开关都没动就别上线：存进集合的应该是「没设过」，不是一堆 false/0。 */
export function emptyOpts(o: Options): boolean {
  return !o.noFollow && !o.insecureTLS && !(o.maxRedirects ?? 0) && !(o.proxy ?? "").trim();
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
    extract: req.extract.filter((e) => e.name.trim() && e.path.trim()).map((e) => ({ ...e })),
    // none 不上线：存进集合的应该是「没用预设」，而不是一份空壳对象
    auth: req.auth.type === "none" ? undefined : { ...req.auth },
    opts: emptyOpts(req.opts) ? undefined : { ...req.opts },
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
