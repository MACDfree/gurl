<script setup lang="ts">
import { computed, onMounted, provide, reactive, ref, watch } from "vue";
import { useDebounceFn, useEventListener } from "@vueuse/core";
import { useMessage } from "naive-ui";
import {
  Send, PickFile, GetHistory, ClearHistory,
  GetEnvState, SaveEnvState, GetCollections, SaveCollections,
  responseDownloadUrl,
} from "./api";
import { formatBody } from "./format";
import type { HTTPResponse, HistoryEntry } from "./types";
import KVEditor from "./components/KVEditor.vue";
import CodeEditor from "./components/CodeEditor.vue";
import CollectionTree from "./components/CollectionTree.vue";
import EnvModal from "./components/EnvModal.vue";
import type { KV } from "./types";
import { methodClass } from "./method";
import { uuid } from "./util";

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

const method = ref("GET");
const url = ref("");
const bodyType = ref<"none" | "json" | "raw" | "form" | "multipart">("raw");
const body = ref("");
const bodyGzip = ref(false);
const timeoutMS = ref(30000);
const params = reactive<KV[]>([]);
const headers = reactive<KV[]>([]);
const formRows = reactive<KV[]>([]);
const fileRows = reactive<{ key: string; path: string }[]>([]);

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

function fillRequest(r: any) {
  if (!r) return;
  method.value = r.method || "GET";
  url.value = r.url || "";
  bodyType.value = r.bodyType || "raw";
  body.value = r.body || "";
  bodyGzip.value = !!r.bodyGzip;
  timeoutMS.value = r.timeoutMs || 30000;
  params.splice(0, params.length, ...(r.params || []));
  headers.splice(0, headers.length, ...(r.headers || []));
  formRows.splice(0, formRows.length, ...(r.form || []));
  fileRows.splice(0, fileRows.length, ...(r.files || []));
  reqTab.value = r.bodyType && r.bodyType !== "none" ? "body" : "params";
}

function restore(e: HistoryEntry) {
  fillRequest((e as any).request);
}

function currentRequest() {
  return {
    method: method.value,
    url: url.value,
    params: JSON.parse(JSON.stringify(params)),
    headers: JSON.parse(JSON.stringify(headers)),
    bodyType: bodyType.value,
    body: body.value,
    form: JSON.parse(JSON.stringify(formRows)),
    files: JSON.parse(JSON.stringify(fileRows.filter((f) => f.key && f.path))),
    bodyGzip: showGzip.value ? bodyGzip.value : false,
    timeoutMs: timeoutMS.value,
  };
}

// ---- 环境变量 ----
const envActive = ref("");
const envs = ref<{ name: string; vars: KV[] }[]>([]);
const envModalOpen = ref(false);

async function loadEnv() {
  try {
    const s = await GetEnvState();
    envActive.value = s?.active || "";
    envs.value = (s?.envs || []).map((e) => ({ name: e.name, vars: e.vars || [] }));
  } catch (e) {
    toolError(e);
  }
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

// ---- 集合 ----
const collections = ref<any[]>([]);
const editingId = ref("");

async function loadCollections() {
  try {
    collections.value = ((await GetCollections()) ?? []).filter(Boolean);
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

function findNode(id: string, nodes: any[] = collections.value): any | null {
  for (const n of nodes) {
    if (n.id === id) return n;
    const hit = findNode(id, n.children || []);
    if (hit) return hit;
  }
  return null;
}

function findParentList(id: string, nodes: any[] = collections.value): any[] | null {
  for (const n of nodes) {
    if ((n.children || []).some((c: any) => c.id === id)) return n.children;
    const hit = findParentList(id, n.children || []);
    if (hit) return hit;
  }
  return null;
}

function addCollection() {
  collections.value.push({ id: uuid(), name: "新建集合", type: "collection", children: [] });
  persistCols();
}

function addFolder(parentId: string) {
  const p = findNode(parentId);
  if (!p) return;
  p.children = p.children || [];
  p.children.push({ id: uuid(), name: "新建目录", type: "folder", children: [] });
  persistCols();
}

function removeNode(id: string) {
  const top = collections.value.findIndex((n) => n.id === id);
  if (top >= 0) {
    collections.value.splice(top, 1);
  } else {
    const list = findParentList(id);
    const i = list?.findIndex((n: any) => n.id === id) ?? -1;
    if (i >= 0) list!.splice(i, 1);
  }
  persistCols();
}

function renameNode(id: string, name: string) {
  const n = findNode(id);
  if (n) {
    n.name = name;
    persistCols();
  }
}

const saveName = ref("");
const saveTarget = ref("");

const targets = computed(() => {
  const out: { id: string; label: string }[] = [];
  const walk = (nodes: any[], depth: number) => {
    for (const n of nodes) {
      if (n.type !== "request") {
        out.push({ id: n.id, label: "　".repeat(depth) + n.name });
        walk(n.children || [], depth + 1);
      }
    }
  };
  walk(collections.value, 0);
  return out;
});

async function saveCurrent() {
  const name = saveName.value.trim() || url.value || "未命名请求";
  const node = { id: uuid(), name, type: "request", request: currentRequest() };
  if (saveTarget.value) {
    const p = findNode(saveTarget.value);
    if (!p) return;
    p.children = p.children || [];
    p.children.push(node);
  } else {
    collections.value.push(node);
  }
  saveName.value = "";
  await persistCols();
}

function openNode(node: any) {
  fillRequest(node.request);
}

provide("colApi", {
  open: openNode,
  remove: removeNode,
  addFolder: addFolder,
  startRename: (id: string) => (editingId.value = id),
  rename: renameNode,
  editingId,
});

onMounted(() => {
  loadEnv();
  loadCollections();
});

function fmtTime(t: string) {
  const d = new Date(t);
  return isNaN(d.getTime()) ? t : d.toLocaleTimeString();
}

const reqTab = ref<"params" | "headers" | "body">("params");
const sending = ref(false);
const resp = ref<HTTPResponse | null>(null);
const errorMsg = ref("");
const respTab = ref<"body" | "headers">("body");
const prettify = ref(true);

async function send() {
  if (sending.value) return;
  sending.value = true;
  errorMsg.value = "";
  resp.value = null;
  try {
    resp.value = await Send({
      method: method.value,
      url: url.value,
      params: [...params],
      headers: [...headers],
      bodyType: bodyType.value,
      body: body.value,
      form: [...formRows],
      files: fileRows.filter((f) => f.key && f.path).map((f) => ({ ...f })),
      bodyGzip: showGzip.value ? bodyGzip.value : false,
      timeoutMs: timeoutMS.value,
    });
    respTab.value = "body";
  } catch (e: any) {
    errorMsg.value = String(e?.message ?? e);
  } finally {
    sending.value = false;
    if (asideOpen.value && panelTab.value === "history") refreshHistory();
  }
}

const statusClass = computed(() => {
  const s = resp.value?.status ?? 0;
  if (s >= 200 && s < 300) return "ok";
  if (s >= 400) return "bad";
  return "warn";
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
      <select v-model="method" class="sel method" title="请求方法">
        <option v-for="m in METHODS" :key="m">{{ m }}</option>
      </select>
      <input v-model="url" class="field url mono" placeholder="https://api.example.com/path" spellcheck="false" @keyup.enter="send" />
      <div class="timeout" title="超时（毫秒）">
        <input v-model.number="timeoutMS" type="number" min="0" step="1000" />
        <span>ms</span>
      </div>
      <button class="btn primary send" :disabled="sending" @click="send">{{ sending ? "发送中" : "发送" }}</button>

      <div class="sep"></div>

      <select v-model="envActive" class="sel env" title="当前环境" @change="persistEnv">
        <option value="">无环境</option>
        <option v-for="e in envs" :key="e.name" :value="e.name">{{ e.name }}</option>
      </select>
      <button class="icon-btn" title="环境管理" @click="envModalOpen = true">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3">
          <circle cx="8" cy="8" r="2.2" />
          <path d="M8 1.6v1.8M8 12.6v1.8M14.4 8h-1.8M3.4 8H1.6M12.5 3.5l-1.3 1.3M4.8 11.2l-1.3 1.3M12.5 12.5l-1.3-1.3M4.8 4.8L3.5 3.5" />
        </svg>
      </button>

      <div class="sep"></div>

      <button class="icon-btn" :class="{ on: asideOpen && panelTab === 'history' }" title="历史" @click="togglePanel('history')">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3">
          <circle cx="8" cy="8" r="6.2" />
          <path d="M8 4.4V8l2.4 1.6" stroke-linecap="round" />
        </svg>
      </button>
      <button class="icon-btn" :class="{ on: asideOpen && panelTab === 'collections' }" title="集合" @click="togglePanel('collections')">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round">
          <path d="M1.9 4.2c0-.7.6-1.3 1.3-1.3h2.6l1.4 1.7h5.6c.7 0 1.3.6 1.3 1.3v5.9c0 .7-.6 1.3-1.3 1.3H3.2c-.7 0-1.3-.6-1.3-1.3V4.2Z" />
        </svg>
      </button>
    </header>

    <div class="main">
      <aside v-if="asideOpen" class="sidebar">
        <div class="side-head">
          <div class="seg grow">
            <button :class="{ on: panelTab === 'history' }" @click="togglePanel('history')">历史</button>
            <button :class="{ on: panelTab === 'collections' }" @click="togglePanel('collections')">集合</button>
          </div>
          <button v-if="panelTab === 'history'" class="icon-btn danger" title="清空历史" @click="clearHistory">
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round">
              <path d="M2.8 4.4h10.4M6.4 4.4V3.1c0-.5.4-.9.9-.9h1.4c.5 0 .9.4.9.9v1.3M4.2 4.4l.6 8c0 .6.5 1 1.1 1h4.2c.6 0 1.1-.4 1.1-1l.6-8" />
            </svg>
          </button>
          <button v-else class="icon-btn" title="新建集合" @click="addCollection">
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
              <path d="M8 3.2v9.6M3.2 8h9.6" />
            </svg>
          </button>
        </div>

        <template v-if="panelTab === 'history'">
          <p v-if="!history.length" class="hint pad">暂无历史记录</p>
          <ul class="hist-list">
            <li v-for="(e, i) in history" :key="i" @click="restore(e)">
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
          <div class="col-save">
            <input v-model="saveName" class="field name-in" placeholder="请求名" />
            <select v-model="saveTarget" class="sel target">
              <option value="">顶层</option>
              <option v-for="t in targets" :key="t.id" :value="t.id">{{ t.label }}</option>
            </select>
            <button class="btn primary" @click="saveCurrent">保存</button>
          </div>
          <div class="col-tree">
            <p v-if="!collections.length" class="hint pad">暂无集合，点上方 ＋ 创建</p>
            <CollectionTree v-for="n in collections" :key="n.id" :node="n" :depth="0" />
          </div>
        </template>
      </aside>

      <div class="panes">
        <section class="card request">
          <div class="card-head">
            <div class="seg">
              <button :class="{ on: reqTab === 'params' }" @click="reqTab = 'params'">Params</button>
              <button :class="{ on: reqTab === 'headers' }" @click="reqTab = 'headers'">Headers</button>
              <button :class="{ on: reqTab === 'body' }" @click="reqTab = 'body'">Body</button>
            </div>
            <span class="grow"></span>
            <template v-if="reqTab === 'body'">
              <select v-model="bodyType" class="sel">
                <option value="none">none</option>
                <option value="json">JSON</option>
                <option value="form">urlencoded</option>
                <option value="multipart">form-data</option>
                <option value="raw">raw</option>
              </select>
              <label v-if="showGzip" class="chk" title="请求体 gzip 压缩后发送">
                <input type="checkbox" v-model="bodyGzip" /> gzip
              </label>
            </template>
          </div>
          <div class="card-body">
            <KVEditor v-if="reqTab === 'params'" :rows="params" />
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
                    <span class="path" :title="row.path">{{ row.path ? row.path.split('/').pop() : '未选择文件' }}</span>
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
              <span class="meta">{{ resp.durationMs }} ms</span>
              <span class="meta">{{ fmtSize(resp.size) }}</span>
            </template>
            <span class="grow"></span>
            <label v-if="resp && resp.isText" class="chk">
              <input type="checkbox" v-model="prettify" /> Pretty
            </label>
            <div v-if="resp" class="seg">
              <button :class="{ on: respTab === 'body' }" @click="respTab = 'body'">Body</button>
              <button :class="{ on: respTab === 'headers' }" @click="respTab = 'headers'">Headers {{ resp.headers?.length ?? 0 }}</button>
            </div>
          </div>
          <div class="card-body">
            <p v-if="errorMsg" class="error">{{ errorMsg }}</p>
            <p v-else-if="!resp" class="hint empty">输入 URL 后按回车或点「发送」</p>
            <template v-else-if="respTab === 'body'">
              <p v-if="!resp.isText" class="hint pad">
                二进制内容（{{ fmtSize(resp.size) }}），不支持文本预览
                <a v-if="resp.bodyId" class="dl" :href="downloadUrl" download>下载完整响应</a>
              </p>
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

/* 原生外观的下拉框 */
.sel {
  appearance: auto;
  background: none;
  border: none;
  color: var(--fg);
  font: inherit;
  font-size: 12px;
  padding: 2px 2px;
}
.sel:focus {
  outline: none;
}
.method {
  font-weight: 600;
}
.env {
  max-width: 120px;
}

.url {
  flex: 1;
  min-width: 0;
  height: 24px;
  padding: 0 8px;
}
.timeout {
  display: flex;
  align-items: center;
  gap: 3px;
  background: var(--bg-panel);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.05);
  padding: 0 6px 0 0;
  height: 24px;
}
.timeout:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--ring);
}
.timeout input {
  width: 52px;
  border: none;
  background: none;
  color: var(--fg);
  font: inherit;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  text-align: right;
  padding: 0 2px 0 7px;
}
.timeout input:focus {
  outline: none;
}
.timeout input::-webkit-outer-spin-button,
.timeout input::-webkit-inner-spin-button {
  -webkit-appearance: none;
  margin: 0;
}
.timeout span {
  font-size: 10.5px;
  color: var(--muted);
}
.send {
  min-width: 62px;
  height: 24px;
  padding: 0 14px;
}

/* ----- 主体 ----- */
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

.col-save {
  flex-shrink: 0;
  display: flex;
  gap: 5px;
  align-items: center;
  padding: 6px 8px;
  border-bottom: 0.5px solid var(--border);
}
.col-save .name-in {
  flex: 1;
  min-width: 0;
  height: 22px;
  padding: 0 6px;
  font-size: 12px;
}
.col-save .target {
  width: 74px;
  flex-shrink: 0;
  font-size: 11.5px;
}
.col-save .btn {
  height: 22px;
  padding: 0 9px;
}
.col-tree {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
  padding: 3px 5px 8px;
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
.cell-in {
  width: 130px;
  flex-shrink: 0;
  background: none;
  border: none;
  border-radius: 4px;
  color: var(--fg);
  font: inherit;
  font-size: 12px;
  padding: 3px 4px;
}
.cell-in:focus {
  outline: none;
  background: #fff;
  box-shadow: 0 0 0 3px var(--ring);
}
.file-row .path {
  flex: 1;
  min-width: 0;
  font-size: 11.5px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
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
</style>
