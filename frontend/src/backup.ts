import type { CollectionNode, Environment, HTTPRequest } from "./types";
import { reqFrom } from "./tabs";
import { uuid } from "./util";

/**
 * 集合 + 环境的整体备份。为什么合成一个文件：这俩是一套配置（集合里的请求满是
 * `{{baseURL}}`，光备份集合等于备份了一堆跑不起来的东西），分两个文件迟早会配错对。
 *
 * 导入方向宽容：gurl 的备份文件、裸集合数组（collections.json）、裸环境状态
 * （env.json）都认；识别不出来就说清楚，不猜。
 */
export const BACKUP_KIND = 1;

export interface BackupFile {
  gurl: number;
  exportedAt: string;
  collections: CollectionNode[];
  env: { active: string; envs: Environment[] };
}

export function buildBackup(collections: CollectionNode[], active: string, envs: Environment[]): string {
  const file: BackupFile = {
    gurl: BACKUP_KIND,
    exportedAt: new Date().toISOString(),
    collections,
    env: { active, envs },
  };
  return JSON.stringify(file, null, 2);
}

export function backupFilename(d = new Date()): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `gurl-backup-${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}.json`;
}

export interface ParsedBackup {
  collections: CollectionNode[];
  envs: Environment[];
  active: string;
  /** 被丢掉或改名的东西，逐条说明：静默修正是最让人没法信任的行为 */
  warnings: string[];
  roots: number;
  requests: number;
}

const NODE_TYPES = new Set(["collection", "folder", "request"]);

function asKVList(v: unknown) {
  return Array.isArray(v) ? (v as { key?: string; value?: string; enabled?: boolean }[]) : [];
}

/** 把一条不知道从哪来的请求规整成能发的样子；缺什么补什么，多余的字段丢掉。 */
function normRequest(raw: unknown): HTTPRequest {
  const r = (raw ?? {}) as Partial<HTTPRequest>;
  const req = reqFrom(r);
  return {
    method: (req.method || "GET").toUpperCase(),
    url: req.url,
    params: req.params,
    headers: req.headers,
    bodyType: req.bodyType,
    body: req.body,
    form: req.form,
    files: req.files,
    bodyGzip: req.bodyGzip,
    timeoutMs: req.timeoutMs,
    extract: req.extract,
  };
}

function normNodes(nodes: unknown, warnings: string[], count: { requests: number }): CollectionNode[] {
  if (!Array.isArray(nodes)) return [];
  const out: CollectionNode[] = [];
  for (const raw of nodes) {
    const n = raw as Partial<CollectionNode>;
    if (!n || typeof n !== "object") {
      warnings.push("跳过了一个空节点");
      continue;
    }
    let type = String(n.type ?? "");
    if (!NODE_TYPES.has(type)) {
      // 没有 type 但带 request 的，按请求处理；其余当目录
      type = n.request ? "request" : "collection";
      warnings.push(`「${String(n.name || "未命名")}」的 type 不认识（${n.type ?? "缺失"}），按 ${type} 处理`);
    }
    const node: CollectionNode = { id: uuid(), name: String(n.name || "").trim() || "未命名", type };
    if (type === "request") {
      node.request = normRequest(n.request);
      if (!node.name || node.name === "未命名") node.name = node.request.url.split("?")[0].split("/").pop() || "未命名";
      count.requests++;
    } else {
      node.children = normNodes(n.children, warnings, count);
    }
    out.push(node);
  }
  return out;
}

function normEnvs(raw: unknown, warnings: string[]): Environment[] {
  if (!Array.isArray(raw)) return [];
  const out: Environment[] = [];
  const seen = new Set<string>();
  for (const e of raw as Partial<Environment>[]) {
    let name = String(e?.name || "").trim();
    if (!name) {
      warnings.push("跳过了一个没有名字的环境");
      continue;
    }
    if (seen.has(name)) {
      let i = 2;
      while (seen.has(`${name} (${i})`)) i++;
      warnings.push(`环境名「${name}」重复，后一个改叫「${name} (${i})」`);
      name = `${name} (${i})`;
    }
    seen.add(name);
    out.push({ name, vars: asKVList(e?.vars).map((v) => ({ key: v.key ?? "", value: v.value ?? "", enabled: v.enabled !== false })) });
  }
  return out;
}

export function parseBackup(text: string): ParsedBackup {
  let data: unknown;
  try {
    data = JSON.parse(text);
  } catch (e) {
    throw new Error(`不是合法的 JSON：${e instanceof Error ? e.message : String(e)}`);
  }
  if (data === null || typeof data !== "object") {
    throw new Error("文件里既不是集合数组也不是环境状态");
  }
  const warnings: string[] = [];
  const count = { requests: 0 };
  let cols: unknown = null;
  let envs: unknown = null;
  let active = "";

  if (Array.isArray(data)) {
    // 裸数组：collections.json 的形状
    cols = data;
  } else {
    const o = data as Record<string, unknown>;
    if (o.gurl !== undefined && o.gurl !== BACKUP_KIND) {
      warnings.push(`备份格式版本是 ${String(o.gurl)}，当前只认 ${BACKUP_KIND}，尽量按能读的部分处理`);
    }
    cols = o.collections ?? o.children ?? null;
    const env = o.env as Record<string, unknown> | undefined;
    if (env || Array.isArray(o.envs)) {
      envs = env ? env.envs : o.envs;
      active = String((env?.active ?? o.active) || "");
    }
    if (cols === null && envs === null) {
      throw new Error("没认出可导入的内容：期望 gurl 备份、集合数组或环境状态");
    }
  }

  return {
    collections: normNodes(cols, warnings, count),
    envs: normEnvs(envs, warnings),
    active,
    warnings,
    roots: Array.isArray(cols) ? countRoots(cols) : 0,
    requests: count.requests,
  };
}

function countRoots(nodes: unknown[]): number {
  return nodes.filter((n) => n && typeof n === "object").length;
}

/** 追加导入时的环境名去重：撞名就把进来的改名，绝不动用户已有的那份。 */
export function dedupeEnvNames(incoming: Environment[], existing: Environment[]): { envs: Environment[]; renamed: string[] } {
  const taken = new Set(existing.map((e) => e.name));
  const renamed: string[] = [];
  const envs = incoming.map((e) => {
    if (!taken.has(e.name)) {
      taken.add(e.name);
      return e;
    }
    let i = 2;
    while (taken.has(`${e.name} (${i})`)) i++;
    const name = `${e.name} (${i})`;
    taken.add(name);
    renamed.push(`${e.name} → ${name}`);
    return { ...e, name };
  });
  return { envs, renamed };
}

/** 数一数里面有多少个请求，导出提示和导入预览都用它。 */
export function countRequests(nodes: CollectionNode[]): number {
  let n = 0;
  for (const node of nodes.filter(Boolean)) {
    if (node.type === "request") n++;
    else n += countRequests((node.children ?? []).filter((c): c is CollectionNode => !!c));
  }
  return n;
}

/** 浏览器里直接下载：gurl 跑在本机，用不着绕后端一圈。 */
export function downloadText(filename: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "application/json" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  // 立刻 revoke 会让某些浏览器来不及取 blob，留一个事件循环
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
