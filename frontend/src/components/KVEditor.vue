<script setup lang="ts">
import type { KV } from "../types";

const props = defineProps<{
  rows: KV[];
}>();

function addRow() {
  props.rows.push({ key: "", value: "", enabled: true });
}

function removeRow(index: number) {
  props.rows.splice(index, 1);
}
</script>

<template>
  <div class="kv-editor">
    <table>
      <thead>
        <tr>
          <th class="c-en"></th>
          <th>Key</th>
          <th>Value</th>
          <th class="c-del"></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(row, i) in rows" :key="i" :class="{ off: !row.enabled }">
          <td class="c-en">
            <input type="checkbox" v-model="row.enabled" />
          </td>
          <td><input class="cell" v-model="row.key" placeholder="Key" spellcheck="false" /></td>
          <td><input class="cell" v-model="row.value" placeholder="Value" spellcheck="false" /></td>
          <td class="c-del">
            <button class="icon-btn danger del" title="删除" @click="removeRow(i)">×</button>
          </td>
        </tr>
      </tbody>
    </table>
    <button class="link" @click="addRow">＋ 添加</button>
  </div>
</template>

<style scoped>
.kv-editor {
  display: flex;
  flex-direction: column;
}
table {
  width: 100%;
  border-collapse: collapse;
}
th {
  text-align: left;
  font-weight: 500;
  font-size: 10.5px;
  letter-spacing: 0.02em;
  color: var(--muted);
  padding: 2px 6px 3px;
  border-bottom: 0.5px solid var(--border);
}
td {
  padding: 0;
  border-bottom: 0.5px solid var(--border);
}
.c-en {
  width: 28px;
  text-align: center;
}
.c-del {
  width: 26px;
  text-align: center;
}
td.c-en {
  padding: 2px 0;
}
tr.off .cell {
  color: var(--muted);
}
.cell {
  width: 100%;
  background: none;
  border: none;
  color: var(--fg);
  font: inherit;
  font-size: 12.5px;
  padding: 4px 6px;
  border-radius: 4px;
}
.cell:focus {
  outline: none;
  background: #fff;
  box-shadow: 0 0 0 3px var(--ring);
}
.del {
  opacity: 0;
  font-size: 14px;
  padding: 1px 4px;
}
tr:hover .del {
  opacity: 1;
}
.link {
  appearance: none;
  background: none;
  border: none;
  color: var(--accent);
  font: inherit;
  font-size: 11.5px;
  padding: 5px 2px 2px;
  cursor: default;
  align-self: flex-start;
}
.link:active {
  opacity: 0.6;
}
</style>
