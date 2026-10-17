<script setup lang="ts">
import { computed, h, nextTick, onMounted, reactive, ref, watch } from "vue";
import { useDebounceFn, useEventListener } from "@vueuse/core";
import { NInputNumber, NPopconfirm, NPopover, NSelect, NTree, NTreeSelect, useMessage } from "naive-ui";
import type { TreeDropInfo, TreeOption } from "naive-ui";
import {
  SendStream, PickFile, GetHistory, ClearHistory,
  responsePreviewUrl,
  GetEnvState, SaveEnvState, GetCollections, SaveCollections,
  GetCookies, SaveCookies,
  responseDownloadUrl,
} from "./api";
import { formatBody } from "./format";
import type { CollectionNode, CookieRecord, HTTPRequest, HTTPResponse, HistoryEntry, SSEEvent } from "./types";
import { contains, findNode, removeFromTree, moveNode, toTreeOptions } from "./collections";
import KVEditor from "./components/KVEditor.vue";
import CodeEditor from "./components/CodeEditor.vue";
import EnvModal from "./components/EnvModal.vue";
import CookieModal from "./components/CookieModal.vue";
import CurlModal from "./components/CurlModal.vue";
import ExtractPanel from "./components/ExtractPanel.vue";
import BackupModal from "./components/BackupModal.vue";
import { backupFilename, buildBackup, countRequests, dedupeEnvNames, downloadText, parseBackup } from "./backup";
import type { ParsedBackup } from "./backup";
import type { KV } from "./types";
import { methodClass } from "./method";
import { emptyRes, newTab, reqFrom, requestOf, snapshot, tabTitle } from "./tabs";
import type { ReqState, ResState, Tab } from "./tabs";
import { scanVars, suggest } from "./vars";
import { uuid } from "./util";

const FOLDER_PATH =
  "M1.9 4.2c0-.7.6-1.3 1.3-1.3h2.6l1.4 1.7h5.6c.7 0 1.3.6 1.3 1.3v5.9c0 .7-.6 1.3-1.3 1.3H3.2c-.7 0-1.3-.6-1.3-1.3V4.2Z";

// 笔尖朝左下（U+270E ✎ 是朝右下的，反着来才对得上视觉习惯）
const PENCIL_PATH = "M12.2 2.4 3 11.5 1.6 14.4 4.5 13 13.6 3.8Z";

const message = useMessage();

/**
 * 错误分两条道：用户正在调的那个请求出错 → 就地显示在响应区（Postman 式）；
 * 工具自己出错（持久化失败、鉴权失效、对话框报错）→ toast。
 * 后者原先被五处「浏览器预览」catch 静默吞掉，保存丢了用户完全不知道。
 */
function toolError(e: unknown) {
  message.error(e instanceof Error ? e.message : String(e));
}

const METHODS = ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"];

const methodOptions = METHODS.map((m) => ({ label: m, value: m }));
// 原生 <option> 上不了色，这正是换 NSelect 的正当理由。
const renderMethodLabel = (option: { label?: string | number }) =>
  h("span", { class: methodClass(String(option.label)), style: "font-weight:600" }, String(option.label));

const bodyTypeOptions = [
  { label: "none", value: "none" },
  { label: "JSON", value: "json" },
  { label: "urlencoded", value: "form" },
  { label: "form-data", value: "multipart" },
  { label: "raw", value: "raw" },
];

function setMethod(v: unknown) {
  method.value = String(v);
}
function setBodyType(v: unknown) {
  bodyType.value = String(v) as typeof bodyType.value;
}
function setTimeoutMS(v: number | null) {
  timeoutMS.value = v ?? 30000;
}
function setEnv(v: unknown) {
  envActive.value = String(v);
  persistEnv();
}

// ---- 标签页 ----
// 下面每个字段都是「当前标签页里那个字段」的可写代理：状态真正住在 tabs 上，
// 切换标签页不再是覆盖一份全局状态，后台还在跑的流也只会写自己那一份。
const tabs = ref<Tab[]>([newTab()]);
const activeId = ref(tabs.value[0].id);
const active = computed(() => tabs.value.find((t) => t.id === activeId.value) ?? tabs.value[0]);

function reqField<K extends keyof ReqState>(k: K) {
  return computed({
    get: () => active.value.req[k],
    set: (v: ReqState[K]) => {
      active.value.req[k] = v;
    },
  });
}
function resField<K extends keyof ResState>(k: K) {
  return computed({
    get: () => active.value.res[k],
    set: (v: ResState[K]) => {
      active.value.res[k] = v;
    },
  });
}

const method = reqField("method");
const url = reqField("url");
const bodyType = reqField("bodyType");
const body = reqField("body");
const bodyGzip = reqField("bodyGzip");
const timeoutMS = reqField("timeoutMs");
const params = reqField("params");
const headers = reqField("headers");
const formRows = reqField("form");
const fileRows = reqField("files");
const extractRows = reqField("extract");

const showGzip = computed(() => bodyType.value === "json" || bodyType.value === "raw");

async function pickFile(row: { key: string; path: string }) {
  try {
    const p = await PickFile();
    if (p) row.path = p;
  } catch (e) {
    toolError(e);
  }
}

const asideOpen = ref(false);
const panelTab = ref<"history" | "collections">("history");
const history = ref<HistoryEntry[]>([]);

async function refreshHistory() {
  try {
    history.value = (await GetHistory()) ?? [];
  } catch (e) {
    toolError(e);
  }
}

function togglePanel(tab: "history" | "collections") {
  if (asideOpen.value && panelTab.value === tab) {
    asideOpen.value = false;
    return;
  }
  asideOpen.value = true;
  panelTab.value = tab;
  if (tab === "history") refreshHistory();
  else loadCollections();
}

async function clearHistory() {
  try {
    await ClearHistory();
    history.value = [];
  } catch (e) {
    // 失败时故意不动本地列表：清掉了但文件还在，重启又冒出来更让人困惑。
    toolError(e);
  }
}

/** 把一份请求装进当前标签页（集合节点、历史条目、cURL 导入都走这里）。 */
function fillRequest(r: Parameters<typeof reqFrom>[0], nodeId = "") {
  const t = active.value;
  t.req = reqFrom(r);
  t.nodeId = nodeId;
  t.saved = snapshot(t.req);
}

function restore(e: HistoryEntry) {
  fillRequest((e as any).request);
}

function currentRequest() {
  return requestOf(active.value.req);
}

/** 有未保存改动：和载入/保存那一刻的快照比字符串，比 deep watch 便宜得多。 */
function tabDirty(t: Tab) {
  return snapshot(t.req) !== t.saved;
}
function titleOf(t: Tab) {
  const node = t.nodeId ? findNode(collections.value, t.nodeId) : null;
  return node?.name || tabTitle(t);
}
function markSaved() {
  active.value.saved = snapshot(active.value.req);
}

function addTab() {
  const t = newTab();
  tabs.value.push(t);
  activeId.value = t.id;
}

function closeTab(id: string) {
  const i = tabs.value.findIndex((t) => t.id === id);
  if (i < 0) return;
  runs.get(id)?.ctl?.abort(); // 关页等于放弃那条流
  runs.delete(id);
  tabs.value.splice(i, 1);
  if (!tabs.value.length) tabs.value.push(newTab());
  if (activeId.value === id) activeId.value = tabs.value[Math.min(i, tabs.value.length - 1)].id;
}

// ---- 环境变量 ----
const envActive = ref("");
const envs = ref<{ name: string; vars: KV[] }[]>([]);
const envOptions = computed(() => [
  { label: "无环境", value: "" },
  ...envs.value.map((e) => ({ label: e.name, value: e.name })),
]);
const envModalOpen = ref(false);

/** 提取出来的运行时变量：后端内存里那份的镜像，界面只读 + 一个清空 */
const runtimeVars = ref<KV[]>([]);

async function loadEnv() {
  try {
    const s = await GetEnvState();
    envActive.value = s?.active || "";
    envs.value = (s?.envs || []).map((e) => ({ name: e.name, vars: e.vars || [] }));
    runtimeVars.value = s?.runtime || [];
  } catch (e) {
    toolError(e);
  }
}

function clearRuntime() {
  // 显式给空数组才是「清空」；PUT 里缺席的含义是「别动它」
  SaveEnvState({ active: envActive.value, envs: envs.value, runtime: [] })
    .then(() => {
      runtimeVars.value = [];
    })
    .catch(toolError);
}

async function persistEnv() {
  try {
    await SaveEnvState({ active: envActive.value, envs: envs.value });
  } catch (e) {
    toolError(e);
  }
}

async function onEnvSave(newEnvs: { name: string; vars: KV[] }[], newActive: string) {
  envs.value = newEnvs;
  envActive.value = newActive;
  envModalOpen.value = false;
  await persistEnv();
}

// ---- 变量使用检查 ----
const envVarNames = computed(() =>
  (envs.value.find((e) => e.name === envActive.value)?.vars ?? [])
    .filter((v) => v.enabled && v.key)
    .map((v) => v.key),
);
// 提取出来的值也是「已定义」，否则每个用 {{token}} 的请求都会被提示条冤枉一次。
const knownVarNames = computed(() => [
  ...new Set([...envVarNames.value, ...runtimeVars.value.map((v) => v.key)]),
]);

// 花括号在模板里拼会被 Vue 的分词器当成插值结束，所以字面量一律在脚本里组好。
const brace = (n: string) => "{{" + n + "}}";

const varHints = computed(() => {
  const { unknown } = scanVars(currentRequest(), knownVarNames.value);
  return unknown.map((name) => {
    const guess = suggest(name, envVarNames.value);
    return { name, raw: brace(name), guess, guessRaw: guess ? brace(guess) : "" };
  });
});

/** 一键改错：把请求里所有 `{{from}}` 换成 `{{to}}`，字段挨个来，不做正则拼接。 */
function fixVar(from: string, to: string) {
  const re = (str: string) => str.split("{{" + from + "}}").join("{{" + to + "}}");
  const req = active.value.req;
  req.url = re(req.url);
  req.body = re(req.body);
  for (const kv of [...req.params, ...req.headers, ...req.form]) {
    kv.key = re(kv.key);
    kv.value = re(kv.value);
  }
  for (const f of req.files) {
    f.key = re(f.key);
    f.path = re(f.path);
  }
}

// ---- Cookie 罐 ----
const cookies = ref<CookieRecord[]>([]);
const cookieModalOpen = ref(false);

async function openCookieModal() {
  try {
    // 先取数据再打开：弹窗只在挂载时复制一次 props，拿到新数据后再挂载才不会是旧快照。
    cookies.value = (await GetCookies()) ?? [];
  } catch (e) {
    toolError(e);
    return;
  }
  cookieModalOpen.value = true;
}

async function onCookieSave(records: CookieRecord[]) {
  cookieModalOpen.value = false;
  cookies.value = records;
  try {
    await SaveCookies(records);
  } catch (e) {
    toolError(e);
  }
}

// ---- 集合 / 环境备份 ----
const backupPending = ref<ParsedBackup | null>(null);
const backupFile = ref<HTMLInputElement | null>(null);

function exportBackup() {
  if (!collections.value.length && !envs.value.length) {
    message.info("集合和环境都是空的，没什么可导出");
    return;
  }
  downloadText(backupFilename(), buildBackup(collections.value, envActive.value, envs.value));
  message.success(
    `已导出 ${collections.value.length} 个顶层集合（${countRequests(collections.value)} 个请求）与 ${envs.value.length} 个环境`,
  );
}

function pickBackup() {
  backupFile.value?.click();
}

async function onBackupFile(e: Event) {
  const input = e.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = ""; // 不清空的话，连续导入同一个文件不会再触发 change
  if (!file) return;
  try {
    backupPending.value = parseBackup(await file.text());
  } catch (err) {
    toolError(err);
  }
}

async function applyBackup(mode: "append" | "replace") {
  const p = backupPending.value;
  if (!p) return;
  backupPending.value = null;
  const cols = mode === "replace" ? p.collections : [...collections.value, ...p.collections];
  let nextEnvs = p.envs;
  const renamed: string[] = [];
  if (mode === "append") {
    const d = dedupeEnvNames(p.envs, envs.value);
    nextEnvs = [...envs.value, ...d.envs];
    renamed.push(...d.renamed);
  }
  // 文件里的「当前环境」只有在替换时才跟过来：追加时用户选中的环境不该被人改掉
  const active = mode === "replace" && nextEnvs.some((e) => e.name === p.active) ? p.active : envActive.value;
  try {
    await SaveCollections(cols);
    await SaveEnvState({ active, envs: nextEnvs });
  } catch (e) {
    toolError(e);
    return;
  }
  collections.value = cols;
  envs.value = nextEnvs.map((e) => ({ name: e.name, vars: e.vars ?? [] }));
  envActive.value = active;
  expandedKeys.value = collectGroupKeys(cols);
  message.success(
    renamed.length
      ? `已导入 ${p.roots} 个顶层集合、${p.envs.length} 个环境；重名环境已改名：${renamed.join("、")}`
      : `已导入 ${p.roots} 个顶层集合、${p.envs.length} 个环境`,
  );
}

// ---- cURL 导入 / 导出 ----
const curlModalOpen = ref(false);
const curlTab = ref<"import" | "export">("import");
const curlRequest = computed(() => currentRequest());

function onCurlImport(r: HTTPRequest) {
  fillRequest(r);
  curlModalOpen.value = false;
  message.success(`已导入 ${r.method} ${r.url}`);
}

// ---- 集合 ----
const collections = ref<CollectionNode[]>([]);
const editingId = ref("");
const draft = ref("");
const selectedKeys = ref<string[]>([]);
const expandedKeys = ref<string[]>([]);
const treeData = computed(() => toTreeOptions(collections.value));

// ---- 侧栏筛选 ----
const filter = ref("");
const filterBox = ref<HTMLInputElement | null>(null);
const needle = computed(() => filter.value.trim().toLowerCase());

/** 历史里能被搜到的：方法、URL、状态码、错误原因 */
const shownHistory = computed(() => {
  const p = needle.value;
  if (!p) return history.value;
  return history.value.filter((e) => `${e.request?.method} ${e.request?.url} ${e.status} ${e.error}`.toLowerCase().includes(p));
});

function nodeHit(p: string, label: unknown, req?: HTTPRequest | null): boolean {
  return `${req?.method ?? ""} ${req?.url ?? ""} ${String(label ?? "")}`.toLowerCase().includes(p);
}

const matchNode = (pattern: string, node: TreeOption) => nodeHit(pattern.trim().toLowerCase(), node.label, (node as GNode).request);

const treeHit = computed(() => {
  const p = needle.value;
  if (!p) return true;
  const walk = (ns: CollectionNode[]): boolean =>
    ns.some((n) => nodeHit(p, n.name, n.request) || walk((n.children ?? []) as CollectionNode[]));
  return walk(collections.value);
});

// 命中项常藏在折叠目录里：一开始筛选就全展开，否则「搜到了却看不见」
watch(filter, (v) => {
  if (v.trim()) expandedKeys.value = collectGroupKeys(collections.value);
});

/** ⌘F 用：面板关着时打开当前页签（开着时不动，避免反过来把它关掉） */
function ensurePanel() {
  if (!asideOpen.value) togglePanel(panelTab.value);
}

// 展开态受控：新建目录时若不展开父节点，新节点藏在折叠里，点了 ＋ 看起来毫无反应。
function setExpanded(keys: Array<string | number>) {
  expandedKeys.value = keys.map(String);
}
function expandNode(id: string) {
  if (!expandedKeys.value.includes(id)) expandedKeys.value = [...expandedKeys.value, id];
}
function collectGroupKeys(nodes: CollectionNode[], out: string[] = []): string[] {
  for (const n of nodes.filter(Boolean)) {
    if (n.type === "request") continue;
    out.push(n.id);
    collectGroupKeys((n.children ?? []).filter((c): c is CollectionNode => !!c), out);
  }
  return out;
}

async function loadCollections() {
  try {
    const got = (await GetCollections()) ?? [];
    collections.value = got.filter((n): n is CollectionNode => !!n);
    expandedKeys.value = collectGroupKeys(collections.value);
  } catch (e) {
    toolError(e);
  }
}

// 每次动树都全量重发整棵树：400ms 内的写合并成一次，省请求，也把「同一页面内连点
// 互相覆盖」的概率压下去。关页面前必须 flush，否则最后一笔编辑会丢在 debounce 里。
const persistCols = useDebounceFn(async () => {
  try {
    await SaveCollections(collections.value);
  } catch (e) {
    toolError(e);
  }
}, 400);
useEventListener(window, "beforeunload", () => void persistCols.flush());

function addCollection() {
  collections.value.push({ id: uuid(), name: "新建集合", type: "collection", children: [] });
  persistCols();
}

function addFolder(parentId: string) {
  const p = findNode(collections.value, parentId);
  if (!p) return;
  expandNode(parentId);
  (p.children ??= []).push({ id: uuid(), name: "新建目录", type: "folder", children: [] });
  beginRename(p.children![p.children!.length - 1]!.id);
  persistCols();
}

function removeNode(id: string) {
  if (!removeFromTree(collections.value, id)) return;
  selectedKeys.value = [];
  persistCols();
}

function beginRename(id: string) {
  const n = findNode(collections.value, id);
  if (!n) return;
  draft.value = n.name;
  editingId.value = id;
  // NTree 把 renderLabel 的返回当函数子节点渲染，实测挂在 input 上的 onInput/onKeydown
  // 不会被 patch 成监听器（元素上没有 _vei），提交永远不触发。所以拿到真实 DOM 自己接线。
  nextTick(() => {
    const el = document.querySelector<HTMLInputElement>(".col-tree input.rename");
    if (!el) return;
    el.addEventListener("input", () => (draft.value = el.value));
    el.addEventListener("keydown", (e) => e.key === "Enter" && el.blur());
    el.addEventListener("blur", commitRename);
    el.focus();
    el.select();
  });
}

function commitRename() {
  const id = editingId.value;
  editingId.value = "";
  const name = draft.value.trim();
  const n = findNode(collections.value, id);
  if (n && name && name !== n.name) {
    n.name = name;
    persistCols();
  }
}

interface GNode extends TreeOption {
  nodeKind?: string;
  request?: HTTPRequest | null;
}

function onSelect(keys: Array<string | number>, opts: Array<TreeOption | null>) {
  const opt = opts[0] as GNode | null | undefined;
  if (!opt || opt.nodeKind !== "request") {
    selectedKeys.value = []; // 点分组只是展开/折叠，不占选中态
    return;
  }
  fillRequest(opt.request, String(opt.key ?? ""));
  selectedKeys.value = keys.map(String);
  reqTab.value = opt.request?.bodyType && opt.request?.bodyType !== "none" ? "body" : "params";
}

function allowDrop({ node, dragNode }: { node: TreeOption; dragNode?: TreeOption }) {
  if (!dragNode) return true;
  const drag = findNode(collections.value, String(dragNode.key));
  return !!drag && !contains(drag, String(node.key)); // 不能把节点塞进自己的子树
}

function onDrop({ node, dragNode, dropPosition }: TreeDropInfo) {
  moveNode(collections.value, String(dragNode.key), String(node.key), dropPosition);
  persistCols();
}

const folderIcon = () =>
  h(
    "svg",
    { class: "ficon", viewBox: "0 0 16 16", fill: "none", stroke: "currentColor", "stroke-width": "1.2", "stroke-linejoin": "round" },
    [h("path", { d: FOLDER_PATH })],
  );

const pencilIcon = () =>
  h(
    "svg",
    { viewBox: "0 0 16 16", fill: "none", stroke: "currentColor", "stroke-width": "1.3", "stroke-linejoin": "round" },
    [h("path", { d: PENCIL_PATH })],
  );

const renderLabel = ({ option, selected }: { option: GNode; selected: boolean }) => {
  if (editingId.value === option.key) {
    // 事件监听在 beginRename 里手动接（见那里的说明），这里只负责渲染
    return h("input", { class: "rename", value: draft.value });
  }
  if (option.nodeKind === "request") {
    return h("span", { class: ["lbl", selected && "on"] }, [
      h("span", { class: ["m", methodClass(option.request?.method)] }, option.request?.method ?? ""),
      h("span", { class: "nm", title: option.request?.url }, String(option.label)),
    ]);
  }
  return h("span", { class: "lbl group" }, [folderIcon(), h("span", { class: "nm" }, String(option.label))]);
};

const renderSuffix = ({ option }: { option: GNode }) =>
  h("span", { class: "acts", onClick: (e: MouseEvent) => e.stopPropagation() }, [
    ...(option.nodeKind === "request"
      ? []
      : [h("button", { class: "icon-btn", title: "新建目录", onClick: () => addFolder(String(option.key)) }, "＋")]),
    h("button", { class: "icon-btn", title: "重命名", onClick: () => beginRename(String(option.key)) }, [pencilIcon()]),
    h("button", { class: "icon-btn danger", title: "删除", onClick: () => removeNode(String(option.key)) }, "×"),
  ]);

// 原来用全角空格伪造缩进，只因为原生 <option> 不能嵌套；NTreeSelect 直接吃树。
// 请求节点不能作为保存目标，跳过。
function targetOptions(nodes: CollectionNode[]): TreeOption[] {
  const out: TreeOption[] = [];
  for (const n of nodes.filter((x): x is CollectionNode => !!x)) {
    if (n.type === "request") continue;
    out.push({
      key: n.id,
      label: n.name,
      children: n.children?.length
        ? targetOptions(n.children.filter((x): x is CollectionNode => !!x))
        : undefined,
    });
  }
  return out;
}
const targetOpts = computed(() => [{ key: "top", label: "顶层" }, ...targetOptions(collections.value)]);

function setTarget(v: unknown) {
  saveTarget.value = !v || v === "top" ? "" : String(v);
}

const saveOpen = ref(false);
const saveName = ref("");
const saveTarget = ref("");
const openNodeId = computed({
  get: () => active.value.nodeId,
  set: (v: string) => {
    active.value.nodeId = v;
  },
});
const openNode = computed(() =>
  openNodeId.value ? findNode(collections.value, openNodeId.value) : null,
);

// 名字默认取 URL 路径末段：整条 URL 当节点名太长且看不出区别。
function suggestedName() {
  const path = url.value.trim().replace(/[?#].*$/, "").replace(/\/+$/, "");
  const last = path.split("/").pop() ?? "";
  return last.replace(/[^A-Za-z0-9._-]/g, "") || "未命名请求";
}

function openSavePopover() {
  saveOpen.value = true;
}

// 预填挂在开关上：无论点触发器还是 ⌘S 打开都会走到。
watch(saveOpen, (v) => {
  if (v) saveName.value = openNode.value?.name ?? suggestedName();
});

/** 新增一份副本到所选位置；之后 ⌘S 就更新这一份。 */
function saveCopy() {
  const name = saveName.value.trim() || suggestedName();
  const node: CollectionNode = { id: uuid(), name, type: "request", request: currentRequest() };
  if (saveTarget.value) {
    const p = findNode(collections.value, saveTarget.value);
    if (!p) return;
    (p.children ??= []).push(node);
  } else {
    collections.value.push(node);
  }
  openNodeId.value = node.id;
  saveOpen.value = false;
  persistCols();
  markSaved();
  message.success(`已保存「${name}」`);
}

/** 就地覆盖打开的那个节点。移动位置是树的拖拽职责，这里不掺和。 */
function saveOverwrite() {
  const n = openNode.value;
  if (!n) return;
  n.request = currentRequest();
  const name = saveName.value.trim();
  if (name && name !== n.name) n.name = name;
  saveOpen.value = false;
  persistCols();
  markSaved();
  message.success(`已更新「${n.name}」`);
}

// ⌘S：从集合里打开的直接覆盖、不打扰；否则弹保存浮层。
function smartSave() {
  if (!url.value.trim()) {
    message.warning("URL 为空");
    return;
  }
  if (openNode.value) {
    saveOverwrite();
    return;
  }
  openSavePopover();
}

useEventListener(window, "keydown", (e: KeyboardEvent) => {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
    e.preventDefault();
    smartSave();
  }
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "f") {
    e.preventDefault();
    ensurePanel();
    nextTick(() => filterBox.value?.select());
  }
});

onMounted(() => {
  loadEnv();
  loadCollections();
});

function fmtTime(t: string) {
  const d = new Date(t);
  return isNaN(d.getTime()) ? t : d.toLocaleTimeString();
}

const reqTab = reqField("reqTab");
const sending = resField("sending");
const resp = resField("resp");
const errorMsg = resField("errorMsg");
const respTab = resField("respTab");
const isStream = resField("isStream");
const events = resField("events");
const shownFrom = resField("shownFrom");
const eventTotal = resField("eventTotal");
const prettify = ref(true);

// ---- 流式（上游 text/event-stream）----
// 只渲染尾部：一次 LLM 回答动辄几千个 event，全塞进 DOM 会把页面钉死，
// 而用户看的总是最后几屏。
const MAX_SHOWN_EVENTS = 500;

/**
 * 每条在跑的请求一份引擎状态，放在组件外的 Map 里而不是标签页对象上：
 * all 故意不进响应式（逐条推给 ref 会把主线程淹掉，每次都要 diff 整个尾部列表），
 * 真正的 DOM 更新按 50ms 合并一次。
 * 用 setTimeout 而不是 requestAnimationFrame：后台标签页里 rAF 是暂停的，那样切走
 * 再切回来之前，面板会一直停在「等待第一个事件」。
 */
interface Run {
  all: SSEEvent[];
  timer: ReturnType<typeof setTimeout> | null;
  ctl: AbortController;
  stopped: boolean;
  openedAt: number;
}
const runs = new Map<string, Run>();

const streamBox = ref<HTMLElement | null>(null);

const hiddenEvents = computed(() => active.value.res.shownFrom);

function flushEvents(t: Tab) {
  const run = runs.get(t.id);
  if (!run) return;
  if (run.timer) {
    clearTimeout(run.timer);
    run.timer = null;
  }
  const r = t.res;
  // 与 events 同一次快照：eventTotal 是同步涨的，用「总数 − 已显示数」算省略条数的话，
  // 节流窗口里会先冒出一句假的「已省略前面 N 条」。
  r.shownFrom = Math.max(0, run.all.length - MAX_SHOWN_EVENTS);
  r.events = run.all.slice(r.shownFrom);
  // 跟着尾巴走：流式面板的价值就是看得见最新一条。用户往上翻会被拉回底部——
  // 要讲究得记滚动位置再判定是否贴底，这个规模下不值得。
  // 只有这一页真的显示在屏幕上时才滚，别去动看不见的那个 DOM。
  if (t.id === activeId.value) {
    nextTick(() => {
      if (streamBox.value) streamBox.value.scrollTop = streamBox.value.scrollHeight;
    });
  }
}

function pushEvent(t: Tab, e: SSEEvent) {
  const run = runs.get(t.id);
  if (!run) return;
  run.all.push(e);
  t.res.eventTotal = run.all.length;
  if (!run.timer) run.timer = setTimeout(() => flushEvents(t), 50);
}

function stop() {
  const run = runs.get(active.value.id);
  if (!run) return;
  run.stopped = true;
  run.ctl.abort();
}

async function send() {
  const t = active.value;
  if (t.res.sending) return;
  const r = t.res;
  const run: Run = { all: [], timer: null, ctl: new AbortController(), stopped: false, openedAt: 0 };
  runs.set(t.id, run);
  Object.assign(r, emptyRes(), { sending: true });
  try {
    await SendStream(
      requestOf(t.req),
      {
        response: (x) => {
          r.resp = x;
          r.respTab = "body";
        },
        meta: (m, timings) => {
          r.isStream = true;
          run.openedAt = performance.now();
          // 状态行和表头仍然由 resp 统一显示；正文是一条条事件，不在这条线上。
          r.resp = {
            status: m.status,
            statusText: m.statusText,
            headers: m.headers,
            body: "",
            isText: false,
            truncated: false,
            durationMs: 0,
            size: 0,
            timings,
          };
          r.respTab = "stream";
        },
        event: (e) => pushEvent(t, e),
        end: ({ durationMs, size, truncated, timings }) => {
          if (r.resp) Object.assign(r.resp, { durationMs, size, truncated, timings });
        },
        fail: (msg) => {
          r.errorMsg = msg;
        },
      },
      run.ctl.signal,
    );
    flushEvents(t); // 收尾把节流窗口里剩的事件刷出来，别让最后几条等下一次定时器
  } catch (e: any) {
    // 用户点「停止」不是一种失败，别拿一行红字盖掉他正在看的事件。
    if (!run.stopped) r.errorMsg = String(e?.message ?? e);
  } finally {
    stampElapsed(t);
    r.sending = false;
    if (r.resp?.extracted) loadEnv(); // 刚提取的值立刻进 knownVarNames，别让提示条继续报错
    if (run.timer) clearTimeout(run.timer);
    runs.delete(t.id);
    // 只有这条流还摆在用户眼前时才刷历史，后台跑完的不该改动他正在看的面板
    if (t.id === activeId.value && asideOpen.value && panelTab.value === "history") refreshHistory();
  }
}

// 手动停止时收不到 end 帧，时长只能在前端补，否则显示成 0 ms。
function stampElapsed(t: Tab) {
  const run = runs.get(t.id);
  const r = t.res.resp;
  if (t.res.isStream && r && !r.durationMs && run && run.openedAt) {
    r.durationMs = Math.round(performance.now() - run.openedAt);
  }
}

const statusClass = computed(() => {
  const s = resp.value?.status ?? 0;
  if (s >= 200 && s < 300) return "ok";
  if (s >= 400) return "bad";
  return "warn";
});

/**
 * 耗时分解。ttfbMs 是从发出请求算起的，已经把 DNS/连接/TLS 包在里面，
 * 所以「等首字节」要减掉前面三段，否则瀑布条会重复计账、比总时长还长。
 */
/**
 * 能在元素里渲染的二进制响应。类型白名单必须和 Go 侧 inlineableMedia 对齐——
 * 那边不肯 inline 的（HTML/SVG/PDF），这里给了 URL 也只会拿到一个下载。
 */
const previewKind = computed(() => {
  const r = resp.value;
  if (!r || r.isText || !r.bodyId) return "";
  const ct = (r.headers?.find((h) => h.key.toLowerCase() === "content-type")?.value ?? "")
    .split(";")[0]
    .trim()
    .toLowerCase();
  if (!ct || ct === "image/svg+xml") return "";
  if (ct.startsWith("image/")) return "image";
  if (ct.startsWith("audio/")) return "audio";
  if (ct.startsWith("video/")) return "video";
  return "";
});

const previewUrl = computed(() => (resp.value?.bodyId ? responsePreviewUrl(resp.value.bodyId) : ""));
const previewName = computed(() => ({ image: "图片", audio: "音频", video: "视频" })[previewKind.value] ?? "");

const phases = computed(() => {
  const t = resp.value?.timings;
  if (!t) return [] as { key: string; name: string; ms: number; pct: number }[];
  const front = (t.dnsMs ?? 0) + (t.connectMs ?? 0) + (t.tlsMs ?? 0);
  const list = [
    { key: "dns", name: "DNS", ms: t.dnsMs ?? 0 },
    { key: "conn", name: "连接", ms: t.connectMs ?? 0 },
    { key: "tls", name: "TLS", ms: t.tlsMs ?? 0 },
    { key: "wait", name: "等首字节", ms: Math.max(0, (t.ttfbMs ?? 0) - front) },
    { key: "body", name: "正文", ms: t.bodyMs ?? 0 },
  ].filter((p) => p.ms > 0);
  const sum = list.reduce((a, p) => a + p.ms, 0);
  return list.map((p) => ({ ...p, pct: sum ? (p.ms / sum) * 100 : 0 }));
});

// 格式化在 Worker 里做：主线程对 5MB 正文 parse + stringify 会卡住几百毫秒。
// Worker 同时告诉我们「是不是合法 JSON」——比猜 Content-Type 准，语法高亮据此开关。
const shownBody = ref("");
const respIsJson = ref(false);
let fmtSeq = 0;

// 超过 1MiB 关掉软换行：压缩过的 JSON 是整整一行，换行等于多秒布局，横向滚动才是对的。
const respWrap = computed(() => (resp.value?.size ?? 0) <= 1 << 20);

watch(
  () => [resp.value?.body, resp.value?.isText, prettify.value] as const,
  async () => {
    const r = resp.value;
    if (!r?.isText) {
      shownBody.value = "";
      respIsJson.value = false;
      return;
    }
    const key = r.bodyId ?? `${r.size}:${r.body.slice(0, 64)}`;
    const my = ++fmtSeq;
    const out = await formatBody(key, r.body);
    if (my !== fmtSeq) return; // 连发请求时，慢回来的格式化不能盖掉新响应
    respIsJson.value = out.isJson;
    shownBody.value = prettify.value ? out.text : r.body;
  },
  { immediate: true },
);

function fmtSize(n?: number) {
  if (n == null) return "";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(2)} MB`;
}

// 与 Go 侧 previewCap 对应，只用来把提示写成人话。
const PREVIEW_CAP = 5 << 20;
const downloadUrl = computed(() =>
  resp.value?.bodyId ? responseDownloadUrl(resp.value.bodyId) : "",
);
</script>

<template>
  <div class="app">
    <header class="toolbar">
      <n-select
        class="tb-sel method"
        size="small"
        :consistent-menu-width="false"
        :value="method"
        :options="methodOptions"
        :render-label="renderMethodLabel"
        @update:value="setMethod"
      />
      <input v-model="url" class="field url mono" placeholder="https://api.example.com/path" spellcheck="false" @keyup.enter="send" />
      <n-input-number
        class="tb-num"
        size="small"
        :show-button="false"
        :min="0"
        :step="1000"
        :value="timeoutMS"
        title="超时（毫秒）"
        @update:value="setTimeoutMS"
      >
        <template #suffix><span class="unit">ms</span></template>
      </n-input-number>
      <!-- 流式请求可能一直挂着，所以「发送中」这一格必须是可点的停止，而不是禁用的标签 -->
      <button class="btn primary send" @click="sending ? stop() : send()">{{ sending ? "停止" : "发送" }}</button>

      <n-popover
        trigger="click"
        placement="bottom-end"
        v-model:show="saveOpen"
        :show-arrow="false"
        :content-style="{ padding: '10px' }"
      >
        <template #trigger>
          <button class="btn send-2" :disabled="!url.trim()" :title="openNode ? '更新到集合 (⌘S)' : '保存到集合 (⌘S)'">保存</button>
        </template>
        <div class="save-pop">
          <input
            v-model="saveName"
            class="field pop-name"
            :placeholder="suggestedName()"
            @keyup.enter="openNode ? saveOverwrite() : saveCopy()"
          />
          <n-tree-select
            class="pop-target"
            size="small"
            :value="saveTarget || null"
            :options="targetOpts"
            placeholder="顶层"
            :disabled="!!openNode"
            :max-menu-height="220"
            :consistent-menu-width="false"
            :menu-props="{ style: 'max-width: 220px' }"
            default-expand-all
            @update:value="setTarget"
          />
          <p class="pop-hint">{{ openNode ? "更新不改位置；要挪动请在树里拖拽" : "留空名字则用 URL 末段" }}</p>
          <div class="pop-acts">
            <button class="btn" @click="saveOpen = false">取消</button>
            <button class="btn primary" @click="openNode ? saveOverwrite() : saveCopy()">
              {{ openNode ? "更新" : "保存" }}
            </button>
          </div>
        </div>
      </n-popover>

      <div class="sep"></div>

      <n-select
        class="tb-sel env"
        size="small"
        :consistent-menu-width="false"
        :value="envActive"
        :options="envOptions"
        title="当前环境"
        @update:value="setEnv"
      />
      <button class="icon-btn" title="环境管理" @click="envModalOpen = true">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3">
          <circle cx="8" cy="8" r="2.2" />
          <path d="M8 1.6v1.8M8 12.6v1.8M14.4 8h-1.8M3.4 8H1.6M12.5 3.5l-1.3 1.3M4.8 11.2l-1.3 1.3M12.5 12.5l-1.3-1.3M4.8 4.8L3.5 3.5" />
        </svg>
      </button>
      <button class="icon-btn" title="Cookie 管理" @click="openCookieModal">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3">
          <path d="M13.6 9.3A5.9 5.9 0 1 1 6.7 2.4a2.5 2.5 0 0 0 2.7 2.6 2.5 2.5 0 0 1 2.6 2.7 2.5 2.5 0 0 0 1.6 1.6Z" />
          <circle cx="6" cy="6.6" r="0.55" fill="currentColor" stroke="none" />
          <circle cx="9" cy="9.8" r="0.55" fill="currentColor" stroke="none" />
          <circle cx="5.6" cy="10" r="0.55" fill="currentColor" stroke="none" />
        </svg>
      </button>

      <div class="sep"></div>

      <button class="icon-btn" title="cURL 导入 / 导出" @click="curlModalOpen = true">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round">
          <path d="M6.2 4.6 3 8l3.2 3.4M9.8 4.6 13 8l-3.2 3.4" />
        </svg>
      </button>

      <button class="icon-btn" :class="{ on: asideOpen && panelTab === 'history' }" title="历史" @click="togglePanel('history')">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3">
          <circle cx="8" cy="8" r="6.2" />
          <path d="M8 4.4V8l2.4 1.6" stroke-linecap="round" />
        </svg>
      </button>
      <button class="icon-btn" title="导出集合与环境备份" @click="exportBackup">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round">
          <path d="M8 2.4v7.2M5.2 7l2.8 2.8L10.8 7M2.8 12.2v1h10.4v-1" />
        </svg>
      </button>
      <button class="icon-btn" title="导入备份" @click="pickBackup">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round">
          <path d="M8 10.4V3.2M5.2 6l2.8-2.8L10.8 6M2.8 12.2v1h10.4v-1" />
        </svg>
      </button>
      <input ref="backupFile" type="file" accept=".json,application/json" class="hidden-file" @change="onBackupFile" />

      <button class="icon-btn" :class="{ on: asideOpen && panelTab === 'collections' }" title="集合" @click="togglePanel('collections')">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round">
          <path d="M1.9 4.2c0-.7.6-1.3 1.3-1.3h2.6l1.4 1.7h5.6c.7 0 1.3.6 1.3 1.3v5.9c0 .7-.6 1.3-1.3 1.3H3.2c-.7 0-1.3-.6-1.3-1.3V4.2Z" />
        </svg>
      </button>
    </header>

    <p v-if="varHints.length" class="var-warn">
      <span class="vw-title">未定义变量</span>
      <span v-for="v in varHints" :key="v.name" class="vw-item">
        <code :title="'这个变量在当前环境里不存在'">{{ v.raw }}</code>
        <template v-if="v.guess">
          想用的不会是
          <button class="vw-fix" :title="'把请求里所有 ' + v.raw + ' 换成 ' + v.guessRaw" @click="v.guess && fixVar(v.name, v.guess)">
            {{ v.guessRaw }}
          </button>
          吧？
        </template>
      </span>
      <button class="vw-goto" @click="envModalOpen = true">{{ envActive ? "改环境" : "先建环境" }}</button>
    </p>

    <div class="main">
      <aside v-if="asideOpen" class="sidebar">
        <div class="side-head">
          <div class="seg grow">
            <button :class="{ on: panelTab === 'history' }" @click="togglePanel('history')">历史</button>
            <button :class="{ on: panelTab === 'collections' }" @click="togglePanel('collections')">集合</button>
          </div>
          <n-popconfirm v-if="panelTab === 'history'" @positive-click="clearHistory">
            <template #trigger>
              <button class="icon-btn danger" title="清空历史">
                <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round">
                  <path d="M2.8 4.4h10.4M6.4 4.4V3.1c0-.5.4-.9.9-.9h1.4c.5 0 .9.4.9.9v1.3M4.2 4.4l.6 8c0 .6.5 1 1.1 1h4.2c.6 0 1.1-.4 1.1-1l.6-8" />
                </svg>
              </button>
            </template>
            清空全部历史记录？
          </n-popconfirm>
          <button v-else class="icon-btn" title="新建集合" @click="addCollection">
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
              <path d="M8 3.2v9.6M3.2 8h9.6" />
            </svg>
          </button>
        </div>

        <div class="filter-wrap">
          <input
            ref="filterBox"
            v-model="filter"
            type="search"
            class="field side-filter"
            :placeholder="panelTab === 'history' ? '筛选历史 (⌘F / Esc 清空)' : '筛选集合 (⌘F / Esc 清空)'"
            spellcheck="false"
            @keydown.esc="filter = ''"
          />
          <button v-if="filter" class="icon-btn filter-x" title="清空筛选" @click="filter = ''">
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
              <path d="M4.2 4.2l7.6 7.6M11.8 4.2l-7.6 7.6" />
            </svg>
          </button>
        </div>

        <template v-if="panelTab === 'history'">
          <p v-if="!history.length" class="hint pad">暂无历史记录</p>
          <p v-else-if="!shownHistory.length" class="hint pad">没有匹配的历史</p>
          <ul class="hist-list">
            <li v-for="(e, i) in shownHistory" :key="i" @click="restore(e)">
              <div class="row1">
                <span class="m" :class="methodClass(e.request?.method)">{{ e.request?.method }}</span>
                <span class="u" :title="e.request?.url">{{ e.request?.url }}</span>
              </div>
              <div class="row2">
                <span>{{ fmtTime(e.time) }}</span>
                <span v-if="e.error" class="st bad">失败</span>
                <span v-else class="st" :class="e.status < 300 ? 'ok' : e.status < 400 ? 'warn' : 'bad'">{{ e.status }}</span>
                <span>{{ e.durationMs }} ms</span>
              </div>
            </li>
          </ul>
        </template>

        <template v-else>
          <div class="col-tree">
            <p v-if="!collections.length" class="hint pad">暂无集合，点上方 ＋ 创建</p>
            <p v-else-if="!treeHit" class="hint pad">没有匹配的集合</p>
            <n-tree
              v-else
              block-line
              :draggable="!needle"
              :cancelable="false"
              :indent="12"
              :data="treeData"
              :pattern="filter"
              :filter="matchNode"
              :show-irrelevant-nodes="false"
              :selected-keys="selectedKeys"
              :expanded-keys="expandedKeys"
              :allow-drop="allowDrop"
              :render-label="renderLabel"
              :render-suffix="renderSuffix"
              @update:selected-keys="onSelect"
              @update:expanded-keys="setExpanded"
              @drop="onDrop"
            />
          </div>
        </template>
      </aside>

      <div class="panes">
        <!-- 一张干净的空页不值得占一行；一旦有未保存改动，＋ 就该看得见 -->
        <div v-if="tabs.length > 1 || tabDirty(tabs[0])" class="tabs">
          <button
            v-for="t in tabs"
            :key="t.id"
            class="tab"
            :class="{ on: t.id === activeId, busy: t.res.sending }"
            :title="(t.nodeId ? '来自集合' : '未保存到集合') + (tabDirty(t) ? ' · 有未保存改动' : '')"
            @click="activeId = t.id"
          >
            <span class="dot" :class="{ dirty: tabDirty(t) }"></span>
            <span class="tn">{{ titleOf(t) }}</span>
            <span class="tx" title="关闭" @click.stop="closeTab(t.id)">×</span>
          </button>
          <button class="tab tadd" title="新标签页" @click="addTab">＋</button>
        </div>

        <section class="card request">
          <div class="card-head">
            <div class="seg">
              <button :class="{ on: reqTab === 'params' }" @click="reqTab = 'params'">Params</button>
              <button :class="{ on: reqTab === 'headers' }" @click="reqTab = 'headers'">Headers</button>
              <button :class="{ on: reqTab === 'body' }" @click="reqTab = 'body'">Body</button>
              <button :class="{ on: reqTab === 'extract' }" @click="reqTab = 'extract'" title="从响应里提取值，给后面的请求用">
                提取 {{ extractRows.length || "" }}
              </button>
            </div>
            <span class="grow"></span>
            <template v-if="reqTab === 'body'">
              <n-select
                class="tb-sel body-type"
                size="small"
                :consistent-menu-width="false"
                :value="bodyType"
                :options="bodyTypeOptions"
                @update:value="setBodyType"
              />
              <label v-if="showGzip" class="chk" title="请求体 gzip 压缩后发送">
                <input type="checkbox" v-model="bodyGzip" /> gzip
              </label>
            </template>
          </div>
          <div class="card-body">
            <ExtractPanel
              v-if="reqTab === 'extract'"
              :rules="extractRows"
              :runtime="runtimeVars"
              @clear="clearRuntime"
            />
            <KVEditor v-else-if="reqTab === 'params'" :rows="params" />
            <KVEditor v-else-if="reqTab === 'headers'" :rows="headers" />
            <template v-else>
              <div v-if="bodyType === 'json' || bodyType === 'raw'" class="editor">
                <CodeEditor
                  v-model="body"
                  :json="bodyType === 'json'"
                  :line-numbers="true"
                  :placeholder="bodyType === 'json' ? '{ &quot;key&quot;: &quot;value&quot; }' : 'Request body (raw)'"
                />
              </div>
              <KVEditor v-else-if="bodyType === 'form'" :rows="formRows" />
              <div v-else-if="bodyType === 'multipart'" class="mp">
                <KVEditor :rows="formRows" />
                <div class="files">
                  <div v-for="(row, i) in fileRows" :key="i" class="file-row">
                    <input class="cell-in" v-model="row.key" placeholder="字段名" />
                    <input class="cell-in path" v-model="row.path" placeholder="文件绝对路径（gurl 所在机器）" spellcheck="false" />
                    <button class="btn" @click="pickFile(row)">选择文件</button>
                    <button class="icon-btn danger" title="移除" @click="fileRows.splice(i, 1)">×</button>
                  </div>
                  <button class="link" @click="fileRows.push({ key: '', path: '' })">＋ 添加文件</button>
                </div>
              </div>
              <p v-else class="hint pad">该请求不发送 Body</p>
            </template>
          </div>
        </section>

        <section class="card response">
          <div class="card-head">
            <span class="card-title">响应</span>
            <template v-if="resp">
              <span class="badge" :class="statusClass">{{ resp.status }}</span>
              <span v-if="isStream" class="badge" :class="sending ? 'warn' : 'ok'">
                {{ sending ? "流式中" : "已结束" }}
              </span>
              <span v-if="!(isStream && sending)" class="meta">{{ resp.durationMs }} ms</span>
              <span class="meta">{{ isStream ? `${eventTotal} 事件` : fmtSize(resp.size) }}</span>
            </template>
            <span class="grow"></span>
            <label v-if="resp && resp.isText" class="chk">
              <input type="checkbox" v-model="prettify" /> Pretty
            </label>
            <div v-if="resp" class="seg">
              <!-- 流式的正文就是事件序列，没有 Body 可看；非流式则压根不出现 Stream -->
              <button v-if="isStream" :class="{ on: respTab === 'stream' }" @click="respTab = 'stream'">
                Events {{ eventTotal }}
              </button>
              <button v-else :class="{ on: respTab === 'body' }" @click="respTab = 'body'">Body</button>
              <button :class="{ on: respTab === 'headers' }" @click="respTab = 'headers'">Headers {{ resp.headers?.length ?? 0 }}</button>
            </div>
          </div>

          <div v-if="phases.length" class="waterfall">
            <div class="wf-bar">
              <span
                v-for="p in phases"
                :key="p.key"
                :class="'wf-' + p.key"
                :style="{ width: p.pct + '%' }"
                :title="p.name + ' ' + p.ms + ' ms'"
              ></span>
            </div>
            <div class="wf-legend">
              <span v-for="p in phases" :key="p.key" class="wf-item">
                <i :class="'wf-' + p.key"></i>{{ p.name }} {{ p.ms }}ms
              </span>
              <span v-if="resp?.timings?.reused" class="wf-note">复用连接</span>
              <span v-if="resp?.timings?.hops" class="wf-note">跟随 {{ resp.timings.hops }} 次跳转</span>
            </div>
          </div>

          <div class="card-body">
            <p v-if="errorMsg && !resp" class="error">{{ errorMsg }}</p>
            <p v-else-if="!resp" class="hint empty">输入 URL 后按回车或点「发送」</p>
            <template v-else-if="isStream">
              <!-- 流到一半出错时，已经收到的事件还要留着看，所以错误是横幅不是排他分支 -->
              <p v-if="errorMsg" class="error">{{ errorMsg }}</p>
              <p v-if="!events.length" class="hint pad">{{ sending ? "已连上，等待第一个事件…" : "没有收到事件" }}</p>
              <div v-else ref="streamBox" class="stream-box mono">
                <p v-if="hiddenEvents" class="hint stream-more">
                  已省略前面 {{ hiddenEvents }} 条，只显示最近 {{ MAX_SHOWN_EVENTS }} 条
                </p>
                <div v-for="(e, i) in events" :key="i" class="sse">
                  <div class="sse-head">
                    <span class="sse-n">{{ hiddenEvents + i + 1 }}</span>
                    <span class="sse-type">{{ e.type }}</span>
                    <span v-if="e.id" class="sse-id">id {{ e.id }}</span>
                  </div>
                  <pre class="sse-data">{{ e.data }}</pre>
                </div>
              </div>
              <p v-if="resp.truncated" class="hint">事件流已达 {{ fmtSize(resp.size) }} 上限，后端停止了读取</p>
            </template>
            <template v-else-if="respTab === 'body'">
              <template v-if="!resp.isText">
                <div v-if="previewKind" class="media">
                  <img v-if="previewKind === 'image'" :src="previewUrl" alt="响应图片" />
                  <audio v-else-if="previewKind === 'audio'" controls :src="previewUrl"></audio>
                  <video v-else controls :src="previewUrl"></video>
                </div>
                <p class="hint pad">
                  <template v-if="previewKind">{{ previewName }}预览（{{ fmtSize(resp.size) }}）</template>
                  <template v-else>二进制内容（{{ fmtSize(resp.size) }}），不支持文本预览</template>
                  <template v-if="resp.bodyId"> · <a class="dl" :href="downloadUrl" download>下载完整响应</a></template>
                </p>
              </template>
              <template v-else>
                <div class="editor viewer">
                  <CodeEditor :model-value="shownBody" readonly :wrap="respWrap" :json="respIsJson" />
                </div>
                <!-- 分支只看 bodyId：双上限之后「被截断」和「有完整正文可取」是两件事 -->
                <p v-if="resp.bodyId" class="hint">
                  页面内只放了前 {{ fmtSize(PREVIEW_CAP) }}，共 {{ fmtSize(resp.size) }} ·
                  <a class="dl" :href="downloadUrl" download>下载完整响应</a>
                </p>
                <p v-else-if="resp.truncated" class="hint">响应超过缓存上限，未保存完整正文</p>
              </template>
            </template>
            <table v-else class="head-table">
              <tbody>
                <tr v-for="(h, i) in resp.headers" :key="i">
                  <td class="k">{{ h.key }}</td>
                  <td class="v">{{ h.value }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </div>

    <EnvModal
      v-if="envModalOpen"
      :active="envActive"
      :envs="envs"
      @save="onEnvSave"
      @close="envModalOpen = false"
    />

    <CookieModal
      v-if="cookieModalOpen"
      :cookies="cookies"
      @save="onCookieSave"
      @close="cookieModalOpen = false"
    />

    <BackupModal
      v-if="backupPending"
      :parsed="backupPending"
      :have-collections="collections.length"
      :have-envs="envs.length"
      @apply="applyBackup"
      @close="backupPending = null"
    />

    <CurlModal
      v-if="curlModalOpen"
      :request="curlRequest"
      :envs="envs"
      :active="envActive"
      :initial="curlTab"
      @import="onCurlImport"
      @tab="curlTab = $event"
      @close="curlModalOpen = false"
    />
  </div>
</template>

<style scoped>
.app {
  display: flex;
  flex-direction: column;
  height: 100vh;
  height: 100dvh;
}

/* ----- 工具栏（毛玻璃） ----- */
.toolbar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  height: 44px;
  padding: 0 10px;
  background: var(--bg-toolbar);
  -webkit-backdrop-filter: saturate(180%) blur(20px);
  backdrop-filter: saturate(180%) blur(20px);
  border-bottom: 0.5px solid var(--border-strong);
}
.sep {
  width: 1px;
  height: 18px;
  background: var(--border-strong);
  margin: 0 3px;
}
.grow {
  flex: 1;
}
.toolbar .icon-btn svg {
  width: 16px;
  height: 16px;
  display: block;
}

/* NSelect / NInputNumber 只借它们的行为，尺寸仍对齐工具栏那 24px 的控件 */
.tb-sel {
  font-size: 12px;
}
.tb-sel :deep(.n-base-selection-label) {
  height: 24px;
  padding: 0 4px;
  font-size: 12px;
}
.method {
  width: 92px;
  flex: none;
  font-weight: 600;
}
.env {
  width: 112px;
  flex: none;
}
.body-type {
  width: 112px;
  flex: none;
}

.url {
  flex: 1;
  min-width: 0;
  height: 24px;
  padding: 0 8px;
}

.tb-num {
  width: 88px;
  flex: none;
}
.tb-num :deep(.n-input__input-el) {
  height: 24px;
  font-size: 12px;
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.unit {
  font-size: 10.5px;
  color: var(--muted);
}
.send {
  min-width: 62px;
  height: 24px;
  padding: 0 14px;
}

/* ----- 主体 ----- */
/* 未定义变量：后端认不出来就原样发出去，所以这属于「会静默出错」，值得占一行 */
.var-warn {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 10px;
  margin: 0;
  padding: 5px 12px;
  font-size: 11.5px;
  color: var(--fg-secondary);
  background: color-mix(in srgb, var(--warn) 12%, var(--bg-panel));
  border-bottom: 0.5px solid var(--border-strong);
}
.vw-title {
  font-weight: 600;
  color: var(--warn);
}
.vw-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.var-warn code {
  padding: 1px 5px;
  border-radius: 4px;
  background: var(--bg-panel);
  font-family: ui-monospace, "SF Mono", Menlo, monospace;
  font-size: 11px;
}
.vw-fix,
.vw-goto {
  appearance: none;
  border: none;
  background: none;
  padding: 0;
  font: inherit;
  color: var(--link);
  cursor: default;
  text-decoration: underline;
  text-underline-offset: 2px;
}
.vw-goto {
  margin-left: auto;
  color: var(--fg-secondary);
}
.main {
  flex: 1;
  display: flex;
  min-height: 0;
}
.panes {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 10px;
  min-width: 0;
  min-height: 0;
}

/* ----- 标签页 ----- */
/* 只在真的需要时才出现：只有一张干净的空页时，这条就是纯粹的噪音 */
.tabs {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 5px;
  overflow-x: auto;
  scrollbar-width: none;
}
.tabs::-webkit-scrollbar {
  display: none;
}
.tab {
  appearance: none;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  max-width: 200px;
  padding: 3px 5px 3px 9px;
  background: var(--bg-panel);
  border: 0.5px solid var(--border-strong);
  border-radius: 999px;
  box-shadow: var(--shadow-card);
  font: inherit;
  font-size: 11.5px;
  color: var(--fg-secondary);
  cursor: default;
}
.tab:hover {
  background: var(--bg-hover);
}
.tab.on {
  border-color: var(--accent);
  color: var(--fg);
  box-shadow: var(--shadow-card), 0 0 0 2px var(--ring);
}
.tab .tn {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tab .dot {
  width: 6px;
  height: 6px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--border-strong);
}
.tab .dot.dirty {
  background: var(--warn);
}
.tab.busy .dot {
  background: var(--accent);
  animation: tabpulse 1s ease-in-out infinite;
}
@keyframes tabpulse {
  50% {
    opacity: 0.25;
  }
}
.tab .tx {
  padding: 0 3px;
  border-radius: 4px;
  color: var(--muted);
  opacity: 0;
}
.tab:hover .tx,
.tab.on .tx {
  opacity: 1;
}
.tab .tx:hover {
  background: var(--bg-hover);
  color: var(--bad);
}
.tadd {
  padding: 3px 10px;
  color: var(--muted);
}

/* ----- 侧栏 ----- */
.sidebar {
  width: 252px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: var(--bg-sidebar);
  -webkit-backdrop-filter: saturate(180%) blur(20px);
  backdrop-filter: saturate(180%) blur(20px);
  border-right: 0.5px solid var(--border-strong);
}
.side-head {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 7px 8px 6px;
}
.side-head .seg {
  display: flex;
}
.side-head .seg button {
  flex: 1;
}
.side-head svg {
  width: 15px;
  height: 15px;
  display: block;
}
.filter-wrap {
  position: relative;
  flex-shrink: 0;
  display: flex;
  margin: 0 8px 6px;
}
.hidden-file {
  display: none;
}
.side-filter {
  flex: 1;
  min-width: 0;
  font-size: 12px;
  padding-right: 22px;
}
.filter-x {
  position: absolute;
  right: 5px;
  top: 50%;
  transform: translateY(-50%);
  width: 14px;
  height: 14px;
  padding: 0;
  color: var(--muted);
}
.filter-x svg {
  width: 9px;
  height: 9px;
}

.hist-list {
  list-style: none;
  margin: 0;
  padding: 2px 5px 8px;
  overflow-y: auto;
  flex: 1;
  min-height: 0;
}
.hist-list li {
  padding: 4px 6px;
  border-radius: 6px;
  cursor: default;
}
.hist-list li:hover {
  background: var(--bg-hover);
}
.hist-list li:active {
  background: rgba(10, 124, 255, 0.16);
}
.hist-list .row1 {
  display: flex;
  gap: 6px;
  align-items: baseline;
}
.hist-list .row1 .m {
  font-size: 9.5px;
  font-weight: 700;
  letter-spacing: 0.01em;
  width: 44px;
  flex-shrink: 0;
}
.hist-list .row1 .u {
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.hist-list .row2 {
  display: flex;
  gap: 8px;
  margin-top: 1px;
  padding-left: 50px;
  font-size: 10.5px;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
.hist-list .st {
  font-weight: 600;
}
.hist-list .st.ok { color: var(--ok); }
.hist-list .st.warn { color: var(--warn); }
.hist-list .st.bad { color: var(--bad); }

/* ----- 保存浮层 ----- */
.send-2 {
  height: 24px;
  padding: 0 12px;
}
.save-pop {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 232px;
}
.pop-name {
  height: 24px;
  font-size: 12px;
}
.pop-target :deep(.n-base-selection-label) {
  height: 24px;
  padding: 0 6px;
  font-size: 11.5px;
}
.pop-hint {
  margin: 0;
  font-size: 10.5px;
  color: var(--muted);
}
.pop-acts {
  display: flex;
  justify-content: flex-end;
  gap: 6px;
}
.pop-acts .btn {
  height: 22px;
  padding: 0 12px;
}
.col-tree {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
  padding: 3px 5px 8px;
}
/* renderLabel / renderSuffix 的节点由 NTree 渲染，不带本组件的 scope 属性，
   所以这里全部走 :deep——直接写 .lbl/.ficon 是一条都不生效的。 */
/* 标签容器默认不收缩，长 URL 会把面板撑开；让它参与收缩才谈得上省略号 */
.col-tree :deep(.n-tree-node-content__text) {
  min-width: 0;
  flex: 1;
}
.col-tree :deep(.n-tree-node-content) {
  font-size: 12px;
}
.col-tree :deep(.lbl) {
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  font-size: 12px;
}
.col-tree :deep(.lbl.group) {
  font-weight: 600;
}
.col-tree :deep(.lbl .m) {
  font-weight: 700;
  font-size: 9.5px;
  width: 42px;
  flex: none;
}
.col-tree :deep(.lbl .nm) {
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.col-tree :deep(.ficon) {
  width: 12px;
  height: 12px;
  flex: none;
  color: #b08a3e;
}
.col-tree :deep(.rename) {
  flex: 1;
  min-width: 0;
  background: #fff;
  border: 1px solid var(--accent);
  border-radius: 4px;
  box-shadow: 0 0 0 3px var(--ring);
  color: var(--fg);
  font-size: 12px;
  padding: 1px 4px;
}
.col-tree :deep(.rename:focus) {
  outline: none;
}
.col-tree :deep(.acts) {
  display: inline-flex;
  opacity: 0;
  flex: none;
  margin-left: 4px;
}
.col-tree :deep(.n-tree-node:hover) .acts {
  opacity: 1;
}
.col-tree :deep(.acts .icon-btn) {
  padding: 1px 3px;
  font-size: 11px;
  line-height: 1;
}
.col-tree :deep(.acts .icon-btn svg) {
  width: 11px;
  height: 11px;
  display: block;
}

/* ----- 卡片 ----- */
.card {
  background: var(--bg-panel);
  border-radius: 8px;
  box-shadow: var(--shadow-card);
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}
.request { flex: 2; }
.response { flex: 3; }
.card-head {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 32px;
  padding: 4px 10px;
  background: linear-gradient(180deg, #ffffff 0%, #f8f8fa 100%);
  border-bottom: 0.5px solid var(--border);
}
/* 耗时分解：段宽按真实占比，一眼看出时间花在建连还是正文 */
.waterfall {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 6px 10px;
  background: var(--bg-inset);
  border-bottom: 0.5px solid var(--border);
}
.wf-bar {
  display: flex;
  height: 5px;
  border-radius: 3px;
  overflow: hidden;
  background: var(--border);
}
.wf-bar span {
  height: 100%;
}
.wf-legend {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 11px;
  color: var(--fg-secondary);
  font-variant-numeric: tabular-nums;
}
.wf-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.wf-item i {
  width: 7px;
  height: 7px;
  border-radius: 2px;
}
.wf-note {
  color: var(--muted);
}
.wf-dns {
  background: var(--accent-dark);
}
.wf-conn {
  background: var(--accent);
}
.wf-tls {
  background: var(--ok);
}
.wf-wait {
  background: var(--warn);
}
.wf-body {
  background: var(--muted);
}
/* 二进制预览：棋盘格垫底，透明 PNG 才看得出边界 */
.media {
  display: flex;
  justify-content: center;
  align-items: flex-start;
  padding: 12px;
  overflow: auto;
  background:
    repeating-conic-gradient(rgba(0, 0, 0, 0.05) 0% 25%, transparent 0% 50%) 0 0 / 16px 16px,
    var(--bg-panel);
}
.media img {
  max-width: 100%;
  max-height: 56vh;
  border-radius: var(--radius);
}
.media audio,
.media video {
  width: 100%;
  max-width: 560px;
}
.card-body {
  flex: 1;
  overflow: auto;
  min-height: 0;
  padding: 4px 8px 8px;
  display: flex;
  flex-direction: column;
}
.card-title {
  font-size: 11px;
  font-weight: 600;
  color: var(--muted);
  letter-spacing: 0.03em;
}
.meta {
  font-size: 11px;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
.chk {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 11.5px;
  color: var(--fg-secondary);
  cursor: default;
  white-space: nowrap;
}

/* ----- 请求体 ----- */
.editor {
  flex: 1;
  min-height: 80px;
  overflow: hidden;
  padding: 2px 4px;
  background: #fbfbfd;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.04);
}
.editor:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--ring);
  background: #fff;
}
/* 响应查看器贴在白卡片上，不需要凹槽 */
.viewer {
  background: transparent;
  border-color: transparent;
  box-shadow: none;
  padding: 0;
}
.mp {
  display: flex;
  flex-direction: column;
}
.files {
  margin-top: 6px;
  border-top: 0.5px solid var(--border);
}
.file-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 2px;
  border-bottom: 0.5px solid var(--border);
}
/* 与 KVEditor 的 .cell 同一套输入框外观 */
.cell-in {
  width: 130px;
  flex-shrink: 0;
  background: var(--bg-inset);
  border: 1px solid var(--border);
  border-radius: 5px;
  color: var(--fg);
  font: inherit;
  font-size: 12px;
  padding: 3px 6px;
}
.cell-in:hover {
  background: rgba(0, 0, 0, 0.065);
}
.cell-in:focus {
  outline: none;
  background: #fff;
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--ring);
}
.file-row .path {
  flex: 1;
  min-width: 0;
  color: var(--fg);
}
.file-row .btn {
  height: 21px;
  padding: 0 8px;
  flex-shrink: 0;
}
.dl {
  color: var(--accent);
  text-decoration: none;
  white-space: nowrap;
}
.dl:hover {
  text-decoration: underline;
}
.link {
  appearance: none;
  background: none;
  border: none;
  color: var(--accent);
  font: inherit;
  font-size: 11.5px;
  padding: 5px 2px;
  cursor: default;
  align-self: flex-start;
}
.link:active {
  opacity: 0.6;
}

/* ----- 响应 ----- */
.empty {
  margin: auto;
  color: var(--muted);
}
.head-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}
.head-table td {
  padding: 3px 6px;
  border-bottom: 0.5px solid var(--border);
  vertical-align: top;
  word-break: break-all;
  line-height: 1.45;
}
.head-table td.k {
  color: var(--fg-secondary);
  font-weight: 500;
  width: 220px;
}
.head-table td.v {
  color: var(--fg);
}
.pad {
  padding: 10px 4px;
}

/* ----- 流式事件 ----- */
.stream-box {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 4px 8px;
  background: #fbfbfd;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.04);
}
.stream-more {
  padding: 0 0 5px;
}
.sse + .sse {
  border-top: 0.5px solid var(--border);
}
.sse-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-top: 5px;
  font-size: 10.5px;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
.sse-type {
  color: var(--accent);
  font-weight: 600;
}
.sse-data {
  margin: 1px 0 5px;
  font: inherit;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
