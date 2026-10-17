<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { NModal } from "naive-ui";
import type { HTTPRequest, KV } from "../types";
import { parseCurl, toCurl } from "../curl";
import CodeEditor from "./CodeEditor.vue";

const props = defineProps<{
  request: HTTPRequest;
  envs: { name: string; vars: KV[] }[];
  active: string;
  initial: "import" | "export";
}>();

const emit = defineEmits<{
  (e: "import", req: HTTPRequest): void;
  (e: "tab", tab: "import" | "export"): void;
  (e: "close"): void;
}>();

const tab = ref<"import" | "export">(props.initial);
// 记住上次停在哪个页签：反复导出的人不该每次都被扔回导入页
watch(tab, (v) => emit("tab", v));

/** 与 Go 侧 envVars() 同一条规则：只取当前环境里启用且有名字的变量 */
const varMap = computed<Record<string, string>>(() => {
  const map: Record<string, string> = {};
  for (const v of props.envs.find((e) => e.name === props.active)?.vars ?? []) {
    if (v.enabled && v.key) map[v.key] = v.value;
  }
  return map;
});
const hasEnv = computed(() => Object.keys(varMap.value).length > 0);
const expand = ref(true);

const command = computed(() => toCurl(props.request, expand.value && hasEnv.value ? varMap.value : undefined));

const SAMPLE = "curl 'https://api.example.com/v1/items?page=2' \\\n  -H 'authorization: Bearer xxx' \\\n  --data-raw '{\"name\":\"a\"}'";
const input = ref("");

const parsed = computed(() => {
  if (!input.value.trim()) return null;
  try {
    const { req, warnings } = parseCurl(input.value);
    return { req, warnings, error: "" };
  } catch (e) {
    return { req: null, warnings: [], error: e instanceof Error ? e.message : String(e) };
  }
});

/** 单独把请求挑出来：模板里对 computed 的联合类型没法自动收窄 */
const summary = computed(() => parsed.value?.req ?? null);
const warnings = computed(() => parsed.value?.warnings ?? []);

const copied = ref(false);
async function copy() {
  const text = command.value;
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    // 非回环地址打开时不是安全上下文，clipboard 不可用；留一条能用的退路
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    document.execCommand("copy");
    ta.remove();
  }
  copied.value = true;
  setTimeout(() => (copied.value = false), 1200);
}

function onShow(v: boolean) {
  if (!v) emit("close");
}
</script>

<template>
  <n-modal :show="true" @update:show="onShow">
    <div class="sheet">
      <header>
        <div class="seg">
          <button :class="{ on: tab === 'import' }" @click="tab = 'import'">导入</button>
          <button :class="{ on: tab === 'export' }" @click="tab = 'export'">导出</button>
        </div>
        <div class="ops">
          <label v-if="tab === 'export'" class="opt">
            <input type="checkbox" v-model="expand" :disabled="!hasEnv" />
            <span :class="{ dim: !hasEnv }">展开 {{ hasEnv ? active : '环境变量' }}</span>
          </label>
          <button v-if="tab === 'export'" class="btn primary" @click="copy">{{ copied ? "已复制" : "复制" }}</button>
          <button v-else class="btn primary" :disabled="!summary" @click="summary && emit('import', summary)">
            导入
          </button>
          <button class="btn" @click="emit('close')">关闭</button>
        </div>
      </header>

      <div class="pane" :class="{ grow: tab === 'import' }">
        <div v-if="tab === 'export'" class="editor viewer">
          <CodeEditor :model-value="command" readonly :wrap="true" />
        </div>
        <div v-else class="editor">
          <CodeEditor v-model="input" :wrap="true" :placeholder="SAMPLE" />
        </div>
      </div>

      <footer v-if="tab === 'import'">
        <p v-if="parsed?.error" class="line bad">{{ parsed.error }}</p>
        <p v-else-if="summary" class="line">
          <span class="m">{{ summary.method }}</span>{{ summary.url }}
          <span v-if="summary.bodyType !== 'none'" class="tag">{{ summary.bodyType }}</span>
          <span class="dim">
            · {{ summary.params?.length ?? 0 }} 参数 · {{ summary.headers?.length ?? 0 }} 头
            <template v-if="summary.files?.length"> · {{ summary.files.length }} 文件</template>
          </span>
        </p>
        <p v-else class="line dim">粘贴一条 curl 命令（浏览器 DevTools 的「Copy as cURL」直接可用）</p>
        <ul v-if="warnings.length" class="warns">
          <li v-for="(w, i) in warnings" :key="i">{{ w }}</li>
        </ul>
      </footer>
      <footer v-else>
        <p class="line dim">
          -L 与 --compressed 对应 gurl 的默认行为（跟随跳转、自动解压）；文件路径是后端所在机器的路径。
        </p>
      </footer>
    </div>
  </n-modal>
</template>

<style scoped>
.sheet {
  width: 720px;
  max-width: 92vw;
  max-height: 80vh;
  background: var(--bg-panel);
  border-radius: 10px;
  box-shadow: var(--shadow-pop);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
header {
  flex-shrink: 0;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  background: linear-gradient(180deg, #ffffff 0%, #f6f6f8 100%);
  border-bottom: 0.5px solid var(--border-strong);
}
.ops {
  display: flex;
  align-items: center;
  gap: 6px;
}
.ops .btn {
  height: 22px;
  padding: 0 12px;
}
.opt {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 11.5px;
  color: var(--fg-secondary);
  cursor: default;
}
.opt input {
  margin: 0;
}
.seg {
  display: flex;
  border: 0.5px solid var(--border-strong);
  border-radius: 6px;
  overflow: hidden;
}
.seg button {
  border: none;
  background: none;
  padding: 3px 12px;
  font: inherit;
  font-size: 11.5px;
  color: var(--fg-secondary);
  cursor: default;
}
.seg button + button {
  border-left: 0.5px solid var(--border-strong);
}
.seg button.on {
  background: var(--accent);
  color: #fff;
}
.pane {
  flex: 1;
  min-height: 0;
  display: flex;
  padding: 8px 10px;
}
.pane.grow {
  min-height: 240px;
}
.editor {
  flex: 1;
  min-height: 220px;
  padding: 2px 4px;
  background: #fbfbfd;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.04);
  overflow: auto;
}
.editor:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--ring);
  background: #fff;
}
/* 导出只供复制，不需要凹槽 */
.viewer {
  background: transparent;
  border-color: transparent;
  box-shadow: none;
  padding: 0;
}
footer {
  flex-shrink: 0;
  border-top: 0.5px solid var(--border);
  background: var(--bg-sidebar);
  padding: 7px 12px;
  font-size: 11.5px;
  color: var(--fg-secondary);
}
.line {
  margin: 0;
  display: flex;
  align-items: baseline;
  gap: 6px;
  word-break: break-all;
}
.line.bad {
  color: var(--bad);
}
.m {
  font-weight: 700;
  font-size: 11px;
}
.tag {
  padding: 0 5px;
  border-radius: 4px;
  background: var(--bg-hover);
  font-size: 10.5px;
}
.dim {
  color: var(--muted);
}
.warns {
  margin: 5px 0 0;
  padding-left: 16px;
  color: var(--warn);
}
.warns li {
  margin: 1px 0;
}
</style>
