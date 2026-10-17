<script setup lang="ts">
import { reactive } from "vue";
import { NCheckbox, NModal } from "naive-ui";
import type { CookieRecord } from "../types";

const props = defineProps<{
  cookies: CookieRecord[];
}>();

const emit = defineEmits<{
  (e: "save", cookies: CookieRecord[]): void;
  (e: "close"): void;
}>();

/** 编辑副本：弹窗内怎么改都不落盘，点「保存」才生效（EnvModal 同款语义）。 */
interface EditRow extends CookieRecord {
  /** 域名列的显示形态：前导点 = 对子域生效，与浏览器 DevTools 一致 */
  domainText: string;
}

function toRow(c: CookieRecord): EditRow {
  return { ...c, domainText: c.hostOnly ? c.domain : "." + c.domain };
}

const rows = reactive<EditRow[]>(props.cookies.map(toRow));

function addRow() {
  rows.push(toRow({ name: "", value: "", domain: "", hostOnly: true, path: "/", secure: false, expires: 0, enabled: true }));
}

function save() {
  const out = rows
    .filter((r) => r.name.trim() && r.domainText.trim())
    .map((r): CookieRecord => {
      const raw = r.domainText.trim().replace(/\.+$/, ""); // 尾点只是格式噪声
      const { domainText, ...rest } = r;
      return { ...rest, hostOnly: !raw.startsWith("."), domain: raw.replace(/^\.+/, "") };
    });
  emit("save", out);
}

function fmtExpires(e: number) {
  return e === 0 ? "会话" : new Date(e * 1000).toLocaleString();
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
        <span class="title">Cookie 管理</span>
        <div class="ops">
          <!-- 不做二次确认：清空只动弹内副本，取消/ESC 不保存即还原；
               而且 naive-ui 的 popconfirm 气泡在 NModal 里会被裁切出幽灵箭头。
               与 KVEditor 的逐行删除同样无确认。 -->
          <button class="btn" :disabled="!rows.length" title="清空列表（需点「保存」才生效）" @click="rows.splice(0, rows.length)">清空</button>
          <button class="btn" @click="emit('close')">取消</button>
          <button class="btn primary" @click="save">保存</button>
        </div>
      </header>
      <div class="body">
        <table v-if="rows.length" class="ck-table">
          <thead>
            <tr>
              <th class="c-en"></th>
              <th class="c-name">名称</th>
              <th>值</th>
              <th class="c-domain">域名</th>
              <th class="c-exp">过期</th>
              <th class="c-del"></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(r, i) in rows" :key="i" :class="{ off: !r.enabled }">
              <td class="c-en"><n-checkbox v-model:checked="r.enabled" size="small" /></td>
              <td><input class="cell" v-model="r.name" placeholder="名称" spellcheck="false" /></td>
              <td><input class="cell" v-model="r.value" :title="r.value" placeholder="值" spellcheck="false" /></td>
              <td><input class="cell" v-model="r.domainText" placeholder="example.com" spellcheck="false" /></td>
              <td class="exp">{{ fmtExpires(r.expires) }}</td>
              <td class="c-del">
                <button class="icon-btn danger del" title="删除" @click="rows.splice(i, 1)">×</button>
              </td>
            </tr>
          </tbody>
        </table>
        <p v-else class="hint pad">还没有 Cookie。发送带 Set-Cookie 的请求会自动捕获到这里，也可以手动添加。</p>
        <button class="link" @click="addRow">＋ 添加 Cookie</button>
        <p class="hint">域名前加 . 表示对子域生效（如 .example.com），不加只对精确主机生效。名称或域名为空的行保存时会被忽略。</p>
      </div>
    </div>
  </n-modal>
</template>

<style scoped>
.sheet {
  width: 760px;
  max-width: 94vw;
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
.ops .btn:disabled {
  opacity: 0.45;
}
.body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 6px 10px 10px;
  display: flex;
  flex-direction: column;
}
.ck-table {
  width: 100%;
  border-collapse: collapse;
}
th {
  text-align: left;
  font-weight: 500;
  font-size: 10.5px;
  letter-spacing: 0.02em;
  color: var(--muted);
  padding: 2px 10px 3px;
  border-bottom: 0.5px solid var(--border);
}
td {
  padding: 3px 4px;
  border-bottom: 0.5px solid var(--border);
}
.c-en {
  width: 28px;
  text-align: center;
}
.c-name {
  width: 150px;
}
.c-domain {
  width: 180px;
}
.c-exp {
  width: 152px;
}
.c-del {
  width: 26px;
  text-align: center;
}
td.c-en {
  padding: 2px 0;
}
/* 输入格外观与 KVEditor 一致：淡灰底 + 淡轮廓，聚焦白底 + 蓝环 */
.cell {
  width: 100%;
  background: var(--bg-inset);
  border: 1px solid var(--border);
  border-radius: 5px;
  color: var(--fg);
  font: inherit;
  font-size: 12.5px;
  padding: 3px 6px;
  text-overflow: ellipsis;
}
.cell:hover {
  background: rgba(0, 0, 0, 0.065);
}
.cell:focus {
  outline: none;
  background: #fff;
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--ring);
}
tr.off .cell {
  color: var(--muted);
}
.exp {
  font-size: 11.5px;
  color: var(--fg-secondary);
  padding-left: 10px;
  white-space: nowrap;
}
tr.off .exp {
  color: var(--muted);
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
.pad {
  padding: 10px 4px;
}
</style>
