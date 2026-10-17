import type { HTTPRequest } from "./types";

/**
 * 后端 subst() 是对 `{{名字}}` 做整串替换，名字里有空格、括号都算「另一个名字」，
 * 认不出来就原样发出去。所以这里也按字面匹配，别替用户把空格 trim 掉——
 * `{{ token }}` 确实不会被替换，提示里就该带上空格让他看出来。
 */
const PATTERN = /\{\{([^{}]*)\}\}/g;

export interface VarUsage {
  /** 请求里出现过的变量名，按出现顺序去重 */
  used: string[];
  /** 当前环境里没有的那些：会原样发出去，多半是拼错 */
  unknown: string[];
}

function eachString(req: HTTPRequest, cb: (s: string) => void) {
  cb(req.url);
  cb(req.body);
  cb(req.opts?.proxy ?? "");
  if (req.auth) {
    for (const v of [req.auth.user, req.auth.pass, req.auth.token, req.auth.key, req.auth.value]) cb(v ?? "");
  }
  for (const list of [req.params, req.headers, req.form]) {
    for (const kv of list ?? []) {
      cb(kv.key);
      cb(kv.value);
    }
  }
  for (const f of req.files ?? []) {
    cb(f.key);
    cb(f.path);
  }
}

export function scanVars(req: HTTPRequest, known: Iterable<string>): VarUsage {
  const set = new Set(known);
  const used: string[] = [];
  const seen = new Set<string>();
  eachString(req, (s) => {
    if (!s || !s.includes("{{")) return;
    for (const m of s.matchAll(PATTERN)) {
      if (!seen.has(m[1])) {
        seen.add(m[1]);
        used.push(m[1]);
      }
    }
  });
  return { used, unknown: used.filter((n) => !set.has(n)) };
}

/**
 * 拼错时给个最接近的名字。只用在提示里：变量表通常就十几条，编辑距离算得起，
 * 而且「未定义」恰恰是最需要补全的时刻——不必为此引入一个补全组件。
 */
export function suggest(name: string, known: string[]): string | null {
  let best: string | null = null;
  let bestD = 3; // 超过 2 的相似度就属于瞎猜
  for (const k of known) {
    const d = editDistance(name, k);
    if (d < bestD) {
      bestD = d;
      best = k;
    }
  }
  return best;
}

function editDistance(a: string, b: string): number {
  if (a === b) return 0;
  let prev = Array.from({ length: b.length + 1 }, (_, i) => i);
  for (let i = 1; i <= a.length; i++) {
    const cur = [i];
    for (let j = 1; j <= b.length; j++) {
      cur[j] = Math.min(
        prev[j] + 1,
        cur[j - 1] + 1,
        prev[j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1),
      );
    }
    prev = cur;
  }
  return prev[b.length];
}
