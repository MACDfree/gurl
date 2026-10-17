<script setup lang="ts">
import type { ExtractRule, KV } from "../types";

const props = defineProps<{
  rules: ExtractRule[];
  runtime: KV[];
}>();

const emit = defineEmits<{ (e: "clear"): void }>();

// 花括号写在模板里会被 Vue 的分词器当成插值结束，所以字面量放在脚本里。
const BRACE_EXAMPLE = "{{名字}}";

function addRow() {
  props.rules.push({ name: "", source: "body", path: "" });
}
function removeRow(i: number) {
  props.rules.splice(i, 1);
}
</script>

<template>
  <div class="ex">
    <table>
      <thead>
        <tr>
          <th class="c-name">变量名</th>
          <th class="c-src">来源</th>
          <th>路径 / 头名</th>
          <th class="c-del"></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(r, i) in rules" :key="i">
          <td>
            <input class="cell mono" v-model="r.name" placeholder="token" spellcheck="false" />
          </td>
          <td>
            <select class="cell" v-model="r.source">
              <option value="body">正文</option>
              <option value="header">表头</option>
            </select>
          </td>
          <td>
            <input
              class="cell mono"
              v-model="r.path"
              :placeholder="r.source === 'header' ? 'X-Trace-Id' : 'data.token 或 items[0].id'"
              spellcheck="false"
            />
          </td>
          <td class="c-del">
            <button class="icon-btn danger del" title="删除" @click="removeRow(i)">×</button>
          </td>
        </tr>
      </tbody>
    </table>
    <button class="link" @click="addRow">＋ 添加提取规则</button>

    <div class="rt">
      <div class="rt-head">
        <span>当前运行时变量</span>
        <button v-if="runtime.length" class="link sm" @click="emit('clear')">清空</button>
      </div>
      <p v-if="!runtime.length" class="hint">
        还没有值。发一次带了规则的请求，取到的值会存在这儿，之后的
        <code>{{ BRACE_EXAMPLE }}</code> 就能用上。
      </p>
      <ul v-else>
        <li v-for="v in runtime" :key="v.key">
          <span class="k mono">{{ v.key }}</span>
          <span class="eq">=</span>
          <span class="val mono" :title="v.value">{{ v.value }}</span>
        </li>
      </ul>
      <p class="note">
        只存在后端内存里：盖过同名环境变量，重启即失效，也不会写进 env.json。
      </p>
    </div>
  </div>
</template>

<style scoped>
.ex {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
table {
  width: 100%;
  border-collapse: collapse;
}
th {
  text-align: left;
  font-weight: 500;
  font-size: 10.5px;
  color: var(--muted);
  padding: 2px 6px;
}
.c-name {
  width: 22%;
}
.c-src {
  width: 96px;
}
.c-del {
  width: 28px;
}
td {
  padding: 2px 3px;
}
.cell {
  width: 100%;
  background: var(--bg-inset);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  color: var(--fg);
  font: inherit;
  font-size: 12px;
  padding: 4px 7px;
}
.cell:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--ring);
  background: #fff;
}
select.cell {
  appearance: none;
  font-size: 11.5px;
}
.mono {
  font-family: ui-monospace, "SF Mono", Menlo, monospace;
}
.del {
  opacity: 0;
}
tr:hover .del {
  opacity: 1;
}
.link {
  appearance: none;
  border: none;
  background: none;
  padding: 2px 0;
  align-self: flex-start;
  font: inherit;
  font-size: 11.5px;
  color: var(--link);
  cursor: default;
}
.link.sm {
  font-size: 11px;
}
.rt {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 0.5px solid var(--border);
}
.rt-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 10.5px;
  color: var(--muted);
  margin-bottom: 4px;
}
.hint {
  margin: 2px 0;
  font-size: 11.5px;
  color: var(--muted);
}
.hint code {
  font-family: ui-monospace, "SF Mono", Menlo, monospace;
}
.rt ul {
  list-style: none;
  margin: 0;
  padding: 0;
  font-size: 11.5px;
}
.rt li {
  display: flex;
  align-items: baseline;
  gap: 6px;
  padding: 1px 0;
}
.rt .k {
  color: var(--accent-dark);
  flex-shrink: 0;
}
.rt .eq {
  color: var(--muted);
}
.rt .val {
  color: var(--fg-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.note {
  margin: 6px 0 0;
  font-size: 11px;
  color: var(--muted);
}
</style>
