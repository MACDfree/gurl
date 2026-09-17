<script setup lang="ts">
import { computed, inject, ref } from "vue";
import type { ColApi } from "../types";
import { methodClass } from "../method";

const props = defineProps<{ node: any; depth: number }>();
const api = inject<ColApi>("colApi")!;

const kids = computed<any[]>(() => (props.node.children ?? []).filter(Boolean));
const expanded = ref(props.node.type !== "request");
const isEditing = computed(() => api.editingId.value === props.node.id);
const draft = ref("");

function beginEdit() {
  draft.value = props.node.name;
  api.startRename(props.node.id);
}

function commit() {
  if (draft.value.trim()) api.rename(props.node.id, draft.value.trim());
  api.startRename("");
}
</script>

<template>
  <div class="node">
    <div
      class="row"
      :style="{ paddingLeft: depth * 12 + 4 + 'px' }"
      @click="node.type === 'request' ? api.open(node) : (expanded = !expanded)"
    >
      <svg v-if="kids.length" class="caret" :class="{ open: expanded }" viewBox="0 0 8 8" fill="currentColor">
        <path d="M2.4 1.1 5.9 4l-3.5 2.9V1.1Z" />
      </svg>
      <span v-else class="caret pad"></span>
      <span v-if="node.type === 'request'" class="m" :class="methodClass(node.request?.method)">{{ node.request?.method }}</span>
      <svg v-else class="ficon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round">
        <path d="M1.9 4.2c0-.7.6-1.3 1.3-1.3h2.6l1.4 1.7h5.6c.7 0 1.3.6 1.3 1.3v5.9c0 .7-.6 1.3-1.3 1.3H3.2c-.7 0-1.3-.6-1.3-1.3V4.2Z" />
      </svg>
      <input
        v-if="isEditing"
        v-model="draft"
        class="rename"
        @keyup.enter="commit"
        @blur="commit"
        @click.stop
      />
      <span
        v-else
        class="name"
        :class="{ group: node.type !== 'request' }"
        :title="node.type === 'request' ? node.request?.url : node.name"
      >{{ node.name }}</span>
      <span class="acts" @click.stop>
        <button v-if="node.type !== 'request'" class="icon-btn" title="新建目录" @click="api.addFolder(node.id)">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"><path d="M8 3.6v8.8M3.6 8h8.8" /></svg>
        </button>
        <button class="icon-btn" title="重命名" @click="beginEdit">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"><path d="M10.6 2.9l2.5 2.5L5.8 12.7 2.6 13.4l.7-3.2 7.3-7.3Z" /></svg>
        </button>
        <button class="icon-btn danger" title="删除" @click="api.remove(node.id)">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"><path d="M4.2 4.2l7.6 7.6M11.8 4.2l-7.6 7.6" /></svg>
        </button>
      </span>
    </div>
    <template v-if="expanded">
      <CollectionTree v-for="c in kids" :key="c.id" :node="c" :depth="depth + 1" />
    </template>
  </div>
</template>

<style scoped>
.row {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 3px 4px;
  border-radius: 6px;
  cursor: default;
  font-size: 12px;
  min-width: 0;
}
.row:hover {
  background: var(--bg-hover);
}
.row:active {
  background: rgba(10, 124, 255, 0.14);
}
.caret {
  width: 8px;
  height: 8px;
  flex-shrink: 0;
  color: var(--muted);
  transition: transform 0.12s ease;
}
.caret.open {
  transform: rotate(90deg);
}
.caret.pad {
  visibility: hidden;
}
.m {
  font-weight: 700;
  font-size: 9.5px;
  width: 42px;
  flex-shrink: 0;
}
.name {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}
.name.group {
  font-weight: 600;
}
.ficon {
  width: 12px;
  height: 12px;
  flex-shrink: 0;
  color: #b08a3e;
}
input.rename {
  flex: 1;
  min-width: 0;
  background: #fff;
  border: 1px solid var(--accent);
  border-radius: 4px;
  box-shadow: 0 0 0 3px var(--ring);
  color: var(--fg);
  font: inherit;
  font-size: 12px;
  padding: 1px 4px;
}
input.rename:focus {
  outline: none;
}
.acts {
  display: none;
  gap: 0;
  flex-shrink: 0;
  margin-left: auto;
}
.row:hover .acts {
  display: inline-flex;
}
.acts .icon-btn {
  padding: 2px 3px;
}
.acts svg {
  width: 12px;
  height: 12px;
  display: block;
}
</style>
