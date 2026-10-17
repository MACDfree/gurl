import type { BodyType, FileKV, HTTPRequest, KV } from "./types";

/** 单引号包裹；里面的单引号按 POSIX 写法换成 '\'' */
function shQuote(s: string): string {
  return "'" + s.replace(/'/g, `'\\''`) + "'";
}

function expand(s: string, vars?: Record<string, string>): string {
  if (!vars) return s;
  let out = s;
  for (const [k, v] of Object.entries(vars)) out = out.split(`{{${k}}}`).join(v);
  return out;
}

/** 与 Go 侧 url.Values.Encode() 对齐：百分号编码，空格写成 + */
function enc(s: string): string {
  return encodeURIComponent(s).replace(/%20/g, "+");
}

function dec(s: string): string {
  try {
    return decodeURIComponent(s.replace(/\+/g, " "));
  } catch {
    return s;
  }
}

export interface CurlParse {
  req: HTTPRequest;
  /** 命令里出现但 gurl 无法照做的部分；宁可逐条说明，也不要静默吞掉一个选项 */
  warnings: string[];
}

// ---- 导出 ----

export function toCurl(req: HTTPRequest, vars?: Record<string, string>): string {
  const ex = (s: string) => expand(s, vars);
  const opts: string[] = [];
  const method = (req.method || "GET").toUpperCase();

  const params = (req.params ?? []).filter((p) => p.enabled && p.key);
  let target = ex(req.url);
  if (params.length) {
    const q = params.map((p) => `${enc(ex(p.key))}=${enc(ex(p.value))}`).join("&");
    target += (target.includes("?") ? "&" : "?") + q;
  }

  if (method !== "GET") opts.push(`-X ${method}`);
  opts.push(shQuote(target));

  const headers = (req.headers ?? []).filter((h) => h.enabled && h.key);
  const has = (name: string) => headers.some((h) => h.key.toLowerCase() === name && h.value.trim());
  for (const h of headers) opts.push(`-H ${shQuote(`${h.key}: ${ex(h.value)}`)}`);

  const type = req.bodyType || "none";
  // application/json 由 gurl 替 json 类型代发，导出时不补就少一个头
  if (type === "json" && req.body && !has("content-type")) opts.push("-H 'Content-Type: application/json'");
  const rows = (req.form ?? []).filter((r) => r.enabled && r.key);
  if (type === "json" || type === "raw") {
    if (req.body) opts.push(`--data-raw ${shQuote(ex(req.body))}`);
  } else if (type === "form") {
    for (const f of rows) opts.push(`-d ${shQuote(`${enc(ex(f.key))}=${enc(ex(f.value))}`)}`);
  } else if (type === "multipart") {
    for (const f of rows) {
      const v = ex(f.value);
      const kv = shQuote(`${ex(f.key)}=${v}`);
      // curl 把值开头的 @ 和 < 当文件引用，字面值只能走 --form-string
      opts.push(/^[@<]/.test(v) ? `--form-string ${kv}` : `-F ${kv}`);
    }
    for (const fl of (req.files ?? []).filter((r) => r.key && r.path)) {
      opts.push(`-F ${shQuote(`${ex(fl.key)}=@${ex(fl.path)}`)}`);
    }
  }

  if (req.timeoutMs > 0) opts.push(`--max-time ${Math.round(req.timeoutMs / 1000)}`);
  // 跟随跳转、自动解压是 gurl 的默认行为（http.Client 与 Transport 都开着）
  opts.push("-L", "--compressed");

  const gzipNote =
    req.bodyGzip && (type === "json" || type === "raw") && req.body
      ? ["# gurl 会把下面的请求体 gzip 后再发（Content-Encoding: gzip）；curl 没有对应选项，这里给的是未压缩原文"]
      : [];
  return [...gzipNote, "curl " + opts.join(" \\\n  ")].join("\n");
}

// ---- 导入 ----

/** POSIX 风格分词：单引号原样、双引号认 \\ \" $ ` 转义、行尾反斜杠续行、词首 # 起注释。 */
function tokenize(src: string): string[] {
  const out: string[] = [];
  let cur = "";
  let started = false;
  let i = 0;
  const n = src.length;
  const flush = () => {
    if (started) out.push(cur);
    cur = "";
    started = false;
  };
  while (i < n) {
    const c = src[i];
    if (c === "#" && !started) {
      while (i < n && src[i] !== "\n") i++;
      continue;
    }
    if (c === " " || c === "\t" || c === "\n" || c === "\r") {
      flush();
      i++;
      continue;
    }
    if (c === "\\") {
      if (src[i + 1] === "\n") i += 2;
      else if (src[i + 1] === "\r" && src[i + 2] === "\n") i += 3;
      else if (i + 1 < n) { cur += src[i + 1]; started = true; i += 2; continue; }
      else i++;
      continue;
    }
    if (c === "'") {
      started = true;
      i++;
      while (i < n && src[i] !== "'") { cur += src[i]; i++; }
      i++;
      continue;
    }
    if (c === '"') {
      started = true;
      i++;
      while (i < n && src[i] !== '"') {
        const e = src[i + 1];
        if (src[i] === "\\" && (e === '"' || e === "\\" || e === "$" || e === "`")) {
          cur += e;
          i += 2;
          continue;
        }
        cur += src[i];
        i++;
      }
      i++;
      continue;
    }
    started = true;
    cur += c;
    i++;
  }
  flush();
  return out;
}

/** 单字母选项全部归到规范长名，下面一张 switch 就能管完所有语义。 */
const SHORT: Record<string, string> = {
  X: "request", H: "header", d: "data", D: "dump-header", b: "cookie", c: "cookie-jar",
  E: "cert", F: "form", G: "get", I: "head", J: "remote-header-name", K: "config",
  k: "insecure", L: "location", M: "multipart-post", N: "no-buffer", O: "remote-name",
  R: "remote-time", S: "show-error", T: "upload-file", U: "upload-delegation", V: "version",
  w: "write-out", a: "auto-referer", A: "user-agent", e: "referer", f: "fail", g: "globoff",
  h: "help", i: "include", j: "junk-session-cookies", m: "max-time", n: "netrc", o: "output",
  p: "proxytunnel", r: "range", s: "silent", t: "telnet-option", u: "user", v: "verbose",
  "1": "tlsv1", "2": "tlsv1.1", "3": "sslv3", "#": "progress-bar",
};

/** 只有列在这里的才允许「下一个参数是它的值」；开关误吞了 URL，整条命令就解析歪了。 */
const LONG_VALUE = new Set([
  "request", "header", "data", "data-raw", "data-binary", "data-ascii", "data-urlencode",
  "json", "form", "form-string", "user", "cookie", "cookie-jar", "user-agent", "referer",
  "url", "max-time", "connect-timeout", "output", "proxy", "proxy-user", "noproxy",
  "resolve", "resolve4", "resolve6", "cert", "cert-type", "key", "key-type", "pass",
  "cacert", "capath", "ciphers", "curves", "tls-max", "oauth2-bearer",
  "retry", "retry-delay", "retry-max-time", "limit-rate", "range", "trace", "trace-ascii",
  "dump-header", "write-out", "doh-url", "local-port", "interface", "netrc-file",
  "max-filesize", "max-redirs", "proto", "proto-redir", "expect100-timeout", "dns-servers",
  "abstract-unix-socket", "unix-socket", "upload-file", "upload-delegation", "alt-svc",
  "connect-to", "hsts", "mail-rcpt", "mail-from", "request-target", "pubkey", "keypubkey",
  "hostpubmd5", "tftp-blksize", "delegation", "aws-sigv4", "config", "telnet-option",
]);

/** gurl 用不上、但不值得打扰用户的噪音选项 */
const IGNORED = new Set([
  "silent", "show-error", "verbose", "no-buffer", "location", "location-trusted", "compressed",
  "fail", "fail-early", "junk-session-cookies", "globoff", "negotiate", "aws-sigv4", "ssl",
  "ssl-reqd", "sslv3", "tlsv1", "tlsv1.1", "http1.1", "http2", "http2-prior-knowledge", "http3",
  "tcp-nodelay", "keepalive", "no-keepalive", "continue", "include", "raw", "ignore-content-meta",
  "ignore-content-type", "retry-all-errors", "version", "help", "unstyled", "style", "disable",
  "disable-ech", "post301", "post302", "post303", "path-as-is", "create-dirs", "stderr",
  "progress-bar", "netrc-optional", "delegation", "gssapi", "krb", "auto-referer", "noproxy",
  "proxytunnel",
]);

/** gurl 确实做不到，值得说一句的 */
const UNSUPPORTED = new Set([
  "connect-timeout", "output", "remote-name", "remote-header-name", "cookie-jar", "proxy",
  "proxy-user", "resolve", "resolve4", "resolve6", "cert", "cert-type", "key", "key-type",
  "pass", "cacert", "capath", "ciphers", "curves", "tls-max", "retry", "retry-delay",
  "retry-max-time", "limit-rate", "range", "trace", "trace-ascii", "dump-header", "write-out",
  "doh-url", "local-port", "interface", "netrc", "netrc-file", "max-filesize", "max-redirs",
  "proto", "proto-redir", "expect100-timeout", "dns-servers", "unix-socket", "abstract-unix-socket",
  "upload-file", "upload-delegation", "multipart-post", "tftp-blksize", "mail-rcpt", "mail-from",
  "connect-to", "alt-svc", "hsts", "request-target", "pubkey", "keypubkey", "hostpubmd5",
  "anyauth", "basic", "digest", "ntlm", "config", "remote-time", "telnet-option",
]);

interface FormEntry {
  text: string;
  /** --form-string 传进来的值不参与 @ / < 的文件语义 */
  literal: boolean;
}

interface Draft {
  method: string;
  getFlag: boolean;
  headFlag: boolean;
  url: string;
  extraUrls: number;
  headers: KV[];
  data: string[];
  json: boolean;
  form: FormEntry[];
  user: string;
  cookie: string;
  agent: string;
  referer: string;
  bearer: string;
  timeoutMs: number;
}

function b64(s: string): string {
  let bin = "";
  for (const byte of new TextEncoder().encode(s)) bin += String.fromCharCode(byte);
  return btoa(bin);
}

/** 从 "k=v" 拆行；只认第一个 =，值里再出现 = 不算分隔。 */
function pair(s: string): KV | null {
  const i = s.indexOf("=");
  if (i <= 0) return null;
  return { key: s.slice(0, i), value: s.slice(i + 1), enabled: true };
}

function queryToParams(q: string): KV[] {
  const out: KV[] = [];
  for (const part of q.split("&")) {
    if (!part) continue;
    const i = part.indexOf("=");
    if (i < 0) out.push({ key: dec(part), value: "", enabled: true });
    else out.push({ key: dec(part.slice(0, i)), value: dec(part.slice(i + 1)), enabled: true });
  }
  return out;
}

export function parseCurl(text: string): CurlParse {
  const args = tokenize(text);
  const warnings: string[] = [];
  const warned = new Set<string>();
  const d: Draft = {
    method: "", getFlag: false, headFlag: false, url: "", extraUrls: 0, headers: [], data: [],
    json: false, form: [], user: "", cookie: "", agent: "", referer: "", bearer: "",
    timeoutMs: 0,
  };

  /** 同一个选项只提醒一次：`curl -o a -o b` 不该刷出两条警告 */
  const note = (key: string, msg: string) => {
    if (warned.has(key)) return;
    warned.add(key);
    warnings.push(msg);
  };

  /** name 已是规范长名；value 为 null 表示要从下一个参数取值。返回消费完的下标。 */
  const setOpt = (name: string, value: string | null, i: number, label: string): number => {
    let val = value ?? "";
    let next = i;
    if (LONG_VALUE.has(name) && value === null) {
      if (i + 1 >= args.length) note(name, `${label} 后面缺少参数`);
      else { val = args[i + 1]; next = i + 1; }
    }
    if (UNSUPPORTED.has(name)) note(name, `${label} 已忽略：gurl 没有对应能力`);
    switch (name) {
      case "request": d.method = val.toUpperCase(); break;
      case "url": if (!d.url) d.url = val; else d.extraUrls++; break;
      case "header": {
        const s = val.trim();
        if (!s) break;
        // -H 'Name;' 是 curl 的「抑制这个头」写法
        if (s.endsWith(";")) { note("header-supp", `-H 'Name;' 用于抑制某个头，导入时忽略`); break; }
        const k = s.indexOf(":");
        if (k < 0) { note("header-bad", `-H ${shQuote(s)} 不是 Key: Value，已忽略`); break; }
        d.headers.push({ key: s.slice(0, k).trim(), value: s.slice(k + 1).trim(), enabled: true });
        break;
      }
      case "data": case "data-raw": case "data-ascii": case "data-binary": case "json":
        if (name === "json") d.json = true;
        if (val.startsWith("@") || val.startsWith("<")) note(name, `${label} ${shQuote(val)} 的正文来自文件，gurl 读不到，已丢弃`);
        else if (val) d.data.push(val);
        break;
      case "data-urlencode": {
        const p = pair(val);
        if (p) d.data.push(`${enc(p.key)}=${enc(p.value)}`);
        else if (val.startsWith("@") || val.startsWith("<")) note(name, `${label} ${shQuote(val)} 的数据来自文件，已丢弃`);
        else if (val) d.data.push(enc(val));
        break;
      }
      case "form": d.form.push({ text: val, literal: false }); break;
      case "form-string": d.form.push({ text: val, literal: true }); break;
      case "user": d.user = val; break;
      case "cookie": {
        // curl 的规则：值里有 = 或 ; 才是 cookie 本身，否则是 cookies.txt 文件名
        if (val.includes("=") || val.includes(";")) d.cookie = val;
        else note(name, `-b ${shQuote(val)} 是 cookie 文件名，gurl 读不到文件，已丢弃`);
        break;
      }
      case "user-agent": d.agent = val; break;
      case "referer": d.referer = val; break;
      case "oauth2-bearer": d.bearer = val; break;
      case "max-time": {
        const sec = Number(val);
        if (Number.isFinite(sec) && sec > 1e-6) d.timeoutMs = Math.min(Math.round(sec * 1000), 6e5);
        break;
      }
      case "get": d.getFlag = true; break;
      case "head": d.headFlag = true; break;
      case "insecure": note(name, "-k（跳过 TLS 证书校验）已忽略：gurl 始终校验证书"); break;
      default:
        if (!IGNORED.has(name)) note(name, `未识别的选项 ${label}，已忽略`);
    }
    return next;
  };

  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (!a) continue;
    if (i === 0 && /^curl(\.exe|\.cmd)?$/i.test(a)) continue;
    if (!a.startsWith("-") || a === "-") {
      if (!d.url) d.url = a;
      else d.extraUrls++;
      continue;
    }
    if (a.startsWith("--")) {
      const eq = a.indexOf("=");
      const name = eq < 0 ? a.slice(2) : a.slice(2, eq);
      const value = eq < 0 ? null : a.slice(eq + 1);
      i = setOpt(name, value, i, `--${name}`);
      continue;
    }
    const body = a.slice(1);
    for (let j = 0; j < body.length; j++) {
      const ch = body[j];
      // 不做大小写回退：curl 的短选项大小写是有意义的，认错会把下一个参数（URL）吞掉
      const name = SHORT[ch] ?? ch;
      const label = `-${ch}`;
      if (LONG_VALUE.has(name)) {
        const rest = body.slice(j + 1);
        i = setOpt(name, rest || null, i, label);
        break;
      }
      setOpt(name, "", i, label);
    }
  }

  if (d.extraUrls) warnings.push("命令里有多个 URL，只取第一个");
  if (!d.url.trim()) throw new Error("没找到 URL：命令里需要一个 http(s) 地址");

  // 请求体：-F 优先于 -d；多个 -d 按 curl 的语义用 & 连接
  let bodyType: BodyType = "none";
  let bodyText = "";
  const formRows: KV[] = [];
  const fileRows: FileKV[] = [];
  if (d.form.length) {
    bodyType = "multipart";
    for (const e of d.form) {
      const p = pair(e.text);
      if (!p) { warnings.push(`-F ${shQuote(e.text)} 不是 key=value，已忽略`); continue; }
      if (!e.literal && p.value.startsWith("@")) {
        let path = p.value.slice(1);
        const semi = path.indexOf(";");
        if (semi >= 0) {
          warnings.push(`-F ${shQuote(e.text)} 里 ;type= / ;filename= 这类附加参数已忽略`);
          path = path.slice(0, semi);
        }
        fileRows.push({ key: p.key, path });
      } else {
        if (!e.literal && p.value.startsWith("<")) {
          warnings.push(`-F ${shQuote(e.text)} 从文件取值，gurl 读不到，值已丢弃`);
          formRows.push({ key: p.key, value: "", enabled: true });
        } else formRows.push(p);
      }
    }
  } else if (d.data.length && !d.getFlag) {
    const rows = d.data.flatMap((s) => s.split("&")).filter(Boolean).map(pair);
    const ct = (d.headers.find((h) => h.key.toLowerCase() === "content-type")?.value ?? "").toLowerCase();
    const isJson = d.json || ct.includes("json");
    // 只有命令自己声明了 JSON 才认 json——bodyType 决定 gurl 代发哪个 Content-Type
    if (rows.length && rows.every((r): r is KV => r !== null) && !isJson) {
      bodyType = "form";
      for (const r of rows) formRows.push({ key: dec(r.key), value: dec(r.value), enabled: true });
    } else {
      bodyText = d.data.join("&");
      bodyType = isJson ? "json" : "raw";
    }
  }

  const params: KV[] = d.getFlag ? d.data.flatMap((s) => queryToParams(s)) : [];

  // URL 自带的 query 拆进 Params，和界面里手写参数走同一条路（发送时由后端统一编码）
  let target = d.url.trim();
  const q = target.indexOf("?");
  if (q >= 0) {
    params.push(...queryToParams(target.slice(q + 1)));
    target = target.slice(0, q);
  }

  const headers: KV[] = [];
  const auth = d.bearer ? `Bearer ${d.bearer}` : d.user ? `Basic ${b64(d.user)}` : "";
  if (d.user && d.bearer) note("auth-conflict", "-u 与 --oauth2-bearer 同时出现，只保留 Bearer");
  for (const h of d.headers) {
    const k = h.key.toLowerCase();
    const v = h.value.toLowerCase();
    // boundary 每次发送都不同，照抄命令里那个只会让上传失败
    if (bodyType === "multipart" && k === "content-type") {
      note("ct-multipart", "命令里的 Content-Type 已丢弃：form-data 的 boundary 由 gurl 生成");
      continue;
    }
    // json / urlencoded 的 Content-Type 由 gurl 代发，留下会在往返之间多出一行
    if (k === "content-type" && ((bodyType === "json" && v === "application/json") || (bodyType === "form" && v === "application/x-www-form-urlencoded"))) {
      continue;
    }
    if (auth && k === "authorization") {
      note("auth-dup", "命令里同时有 Authorization 头和 -u / --oauth2-bearer，保留后者");
      continue;
    }
    headers.push(h);
  }
  if (auth) headers.push({ key: "Authorization", value: auth, enabled: true });
  if (d.cookie) headers.push({ key: "Cookie", value: d.cookie, enabled: true });
  if (d.agent) headers.push({ key: "User-Agent", value: d.agent, enabled: true });
  if (d.referer) headers.push({ key: "Referer", value: d.referer, enabled: true });

  const method = d.method || (d.getFlag ? "GET" : d.headFlag ? "HEAD" : bodyType !== "none" ? "POST" : "GET");

  const req: HTTPRequest = {
    method,
    url: target,
    params,
    headers,
    bodyType,
    body: bodyText,
    form: formRows,
    files: fileRows,
    bodyGzip: false,
    timeoutMs: d.timeoutMs || 30000,
  };
  return { req, warnings };
}
