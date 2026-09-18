<script setup lang="ts">
import { reactive, ref } from "vue";
import { NModal } from "naive-ui";
import KVEditor from "./KVEditor.vue";
import type { KV } from "../types";

const props = defineProps<{
  active: string;
  envs: { name: string; vars: KV[] }[];
}>();

const emit = defineEmits<{
  (e: "save", envs: { name: string; vars: KV[] }[], active: string): void;
  (e: "close"): void;
}>();

const local = reactive({
  active: props.active,
  envs: props.envs.map((en) => ({ name: en.name, vars: en.vars.map((v) => ({ ...v })) })),
});
const selected = ref(0);

function addEnv() {
  local.envs.push({ name: `env${local.envs.length + 1}`, vars: [] });
  selected.value = local.envs.length - 1;
}

function removeEnv(i: number) {
  local.envs.splice(i, 1);
  if (selected.value >= local.envs.length) selected.value = Math.max(0, local.envs.length - 1);
  if (local.active === (local.envs[i]?.name ?? "")) local.active = "";
}

// Esc 与点遮罩由 NModal 处理，统一转成既有的 close 事件。
function onShow(v: boolean) {
  if (!v) emit("close");
}
</script>

<template>
  <n-modal :show="true" @update:show="onShow">
    <div class="sheet">
      <header>
        <span class="title">环境管理</span>
        <div class="ops">
          <button class="btn" @click="emit('close')">取消</button>
          <button class="btn primary" @click="emit('save', local.envs, local.active)">保存</button>
        </div>
      </header>
      <div class="body">
        <ul class="envs">
          <li
            v-for="(en, i) in local.envs"
            :key="i"
            :class="{ sel: i === selected }"
            @click="selected = i"
          >
            <input v-model="en.name" class="ename" @click.stop="selected = i" />
            <button
              class="icon-btn act"
              :class="{ on: local.active === en.name }"
              title="设为当前环境"
              @click.stop="local.active = local.active === en.name ? '' : en.name"
            >
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                <path d="M3 8.4 6.3 11.7 13 4.6" />
              </svg>
            </button>
            <button class="icon-btn danger del" title="删除环境" @click.stop="removeEnv(i)">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"><path d="M4.2 4.2l7.6 7.6M11.8 4.2l-7.6 7.6" /></svg>
            </button>
          </li>
          <li class="add" @click="addEnv">＋ 新建环境</li>
        </ul>
        <div class="vars">
          <p class="hint pad" v-if="!local.envs[selected]">选择或新建一个环境</p>
          <KVEditor v-else :rows="local.envs[selected].vars" />
        </div>
      </div>
    </div>
  </n-modal>
</template>

<style scoped>
.sheet {
  width: 620px;
  max-width: 92vw;
  max-height: 78vh;
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
  padding: 9px 12px;
  background: linear-gradient(180deg, #ffffff 0%, #f6f6f8 100%);
  border-bottom: 0.5px solid var(--border-strong);
}
.title {
  font-size: 12.5px;
  font-weight: 600;
}
.ops {
  display: flex;
  gap: 6px;
}
.ops .btn {
  height: 22px;
  padding: 0 12px;
}
.body {
  flex: 1;
  display: flex;
  min-height: 0;
}
ul.envs {
  width: 186px;
  flex-shrink: 0;
  list-style: none;
  margin: 0;
  padding: 5px;
  background: var(--bg-sidebar);
  border-right: 0.5px solid var(--border);
  overflow-y: auto;
}
ul.envs li {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 3px 4px;
  border-radius: 6px;
  font-size: 12px;
  cursor: default;
}
ul.envs li:hover {
  background: var(--bg-hover);
}
ul.envs li.sel {
  background: var(--bg-selected);
  box-shadow: 0 1px 3px rgba(10, 124, 255, 0.35);
}
ul.envs li.sel .ename {
  color: #fff;
}
ul.envs li.sel .icon-btn {
  color: rgba(255, 255, 255, 0.82);
}
ul.envs li.sel .icon-btn:hover {
  background: rgba(255, 255, 255, 0.18);
  color: #fff;
}
input.ename {
  flex: 1;
  min-width: 0;
  background: none;
  border: none;
  border-radius: 4px;
  color: var(--fg);
  font: inherit;
  font-size: 12px;
  padding: 2px 4px;
}
input.ename:focus {
  outline: none;
  background: #fff;
  color: var(--fg);
  box-shadow: 0 0 0 3px var(--ring);
}
/* 选中行是蓝底白字：聚焦时若照铺白底，白字就看不见了（li.sel 的白字特异性更高） */
ul.envs li.sel input.ename:focus {
  background: rgba(255, 255, 255, 0.18);
  color: #fff;
  caret-color: #fff;
  box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.55);
}
.act {
  padding: 2px;
  opacity: 0.45;
}
.act.on {
  opacity: 1;
}
li:hover .act {
  opacity: 1;
}
.act svg, .del svg {
  width: 12px;
  height: 12px;
  display: block;
}
.del {
  padding: 2px;
  opacity: 0;
}
li:hover .del {
  opacity: 1;
}
li.add {
  color: var(--accent);
  font-size: 11.5px;
  padding: 5px 6px;
}
li.add:hover {
  background: var(--bg-hover);
}
.vars {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: 6px 8px;
}
.pad {
  padding: 10px 4px;
}
</style>
