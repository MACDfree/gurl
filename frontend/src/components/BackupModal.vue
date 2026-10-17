<script setup lang="ts">
import { ref } from "vue";
import { NModal } from "naive-ui";
import type { ParsedBackup } from "../backup";

defineProps<{
  parsed: ParsedBackup;
  /** 现有内容数量：让「替换」这一步看得见代价 */
  haveCollections: number;
  haveEnvs: number;
}>();

const emit = defineEmits<{
  (e: "apply", mode: "append" | "replace"): void;
  (e: "close"): void;
}>();

/**
 * 「替换全部」要按两次。实测过一次误触发：弹窗里三个按钮并排，一次多余的点击就能把
 * 用户现有的集合与环境整体换掉，而这一步不备份就没法回退。
 */
const armed = ref(false);

function replace() {
  if (!armed.value) {
    armed.value = true;
    return;
  }
  emit("apply", "replace");
}

function onShow(v: boolean) {
  if (!v) emit("close");
}
</script>

<template>
  <n-modal :show="true" @update:show="onShow">
    <div class="sheet">
      <header>
        <span class="title">导入备份</span>
        <div class="ops">
          <button class="btn" @click="emit('close')">取消</button>
          <button class="btn danger" :class="{ armed }" @click="replace">
            {{ armed ? "确认替换？" : "替换全部" }}
          </button>
          <button class="btn primary" @click="emit('apply', 'append')">追加导入</button>
        </div>
      </header>
      <div class="body">
        <ul class="sum">
          <li>
            集合：<b>{{ parsed.roots }}</b> 个顶层节点，含 <b>{{ parsed.requests }}</b> 个请求
          </li>
          <li>
            环境：<b>{{ parsed.envs.length }}</b> 个<template v-if="parsed.active">（文件里的当前环境：{{ parsed.active }}）</template>
          </li>
        </ul>
        <ul v-if="parsed.warnings.length" class="warns">
          <li v-for="(w, i) in parsed.warnings" :key="i">{{ w }}</li>
        </ul>
        <p class="note">
          <b>追加</b>：现有内容一律保留，节点拿新 id 挂在顶层；环境撞名会把进来的改名，
          不动你已有的那份。<br />
          <b>替换</b>：用文件内容整体覆盖现在的 {{ haveCollections }} 个顶层集合与
          {{ haveEnvs }} 个环境，这一步不备份就没法回退。
        </p>
        <p v-if="armed" class="note armed">
          再点一次「确认替换？」才会真的执行；想收手点取消。
        </p>
        <p class="note">
          两种做法都不碰运行时变量（那是上一次响应提取出来的值，重启本来就没）。
        </p>
      </div>
    </div>
  </n-modal>
</template>

<style scoped>
.sheet {
  width: 480px;
  max-width: 92vw;
  background: var(--bg-panel);
  border-radius: 10px;
  box-shadow: var(--shadow-pop);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
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
/* 全局只有 .icon-btn.danger；这里不补一层红，破坏性按钮就和「取消」长得一样了 */
.ops .btn.danger {
  color: var(--bad);
  border-color: color-mix(in srgb, var(--bad) 40%, var(--border-strong));
}
.ops .btn.danger:hover {
  background: rgba(215, 0, 21, 0.08);
}
.body {
  padding: 10px 12px 12px;
}
.sum {
  margin: 0;
  padding-left: 18px;
  font-size: 12px;
  color: var(--fg);
}
.sum li {
  margin: 2px 0;
}
.warns {
  margin: 8px 0 0;
  padding-left: 18px;
  font-size: 11.5px;
  color: var(--warn);
}
.ops .btn.danger.armed {
  background: var(--bad);
  border-color: var(--bad);
  color: #fff;
}
.note.armed {
  color: var(--bad);
  font-weight: 600;
}
.note {
  margin: 9px 0 0;
  font-size: 11.5px;
  line-height: 1.6;
  color: var(--fg-secondary);
}
</style>
