<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useDialog, useMessage } from "naive-ui";
import { ApplyUpdate, CheckUpdate, GetVersion, type UpdateInfo } from "../api";

const message = useMessage();
const dialog = useDialog();

const busy = ref(false); // 正在下载替换，或重启轮询中
const pending = ref<UpdateInfo | null>(null); // 有可用更新时给按钮加高亮

const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));

async function check(silent: boolean) {
  if (busy.value) return;
  let info: UpdateInfo;
  try {
    info = await CheckUpdate();
  } catch (e) {
    if (!silent) message.error(`检查更新失败：${errMsg(e)}`);
    return; // 启动时的静默检查失败不打扰
  }
  pending.value = info.hasUpdate ? info : null;
  if (info.hasUpdate) {
    confirmUpdate(info);
    return;
  }
  if (silent) return;
  if (!info.latest) {
    message.info("仓库还没有发布任何版本");
  } else if (info.disabled) {
    message.warning(`开发构建不支持自更新，最新是 ${info.latest}，请手动下载`);
  } else {
    message.info(`已是最新版本 ${info.current}`);
  }
}

function confirmUpdate(info: UpdateInfo) {
  dialog.info({
    title: "发现新版本",
    content: `${info.current} → ${info.latest}\n将自动下载并替换当前程序，然后原地重启（页面会自己刷新）。`,
    positiveText: "立即更新",
    negativeText: "稍后",
    onPositiveClick: () => doUpdate(info),
  });
}

async function doUpdate(info: UpdateInfo) {
  busy.value = true;
  try {
    await ApplyUpdate();
  } catch (e) {
    busy.value = false;
    message.error(`更新失败：${errMsg(e)}`);
    return;
  }
  message.loading("已更新，正在重启…", { duration: 0 });
  await waitRestart(info.latest);
}

// 旧进程收到 apply 响应后会 shutdown 再起新进程，中间有一段连不上的窗口期，
// 这里的失败一律静默重试，别把 api.ts 的「连不上本地后端」弹给用户。
async function waitRestart(latest: string) {
  const deadline = Date.now() + 20000;
  for (;;) {
    await new Promise((r) => setTimeout(r, 500));
    try {
      const v = await GetVersion();
      if (v.version === latest) {
        location.reload();
        return;
      }
    } catch {
      /* 重启窗口期，继续等 */
    }
    if (Date.now() > deadline) {
      busy.value = false;
      message.destroyAll();
      message.error("重启超时，请手动刷新页面确认");
      return;
    }
  }
}

onMounted(() => check(true));
</script>

<template>
  <button
    class="icon-btn update-btn"
    :class="{ on: !!pending, spin: busy }"
    :disabled="busy"
    :title="pending ? `有新版本 ${pending.latest}` : '检查更新'"
    @click="check(false)"
  >
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round">
      <path d="M13.4 8a5.4 5.4 0 1 1-1.6-3.8" />
      <path d="M13.6 1.8v2.9h-2.9" />
    </svg>
    <span v-if="pending" class="dot"></span>
  </button>
</template>

<style scoped>
.update-btn {
  position: relative;
}
.update-btn svg {
  width: 16px;
  height: 16px;
  display: block;
}
.update-btn:disabled {
  cursor: default;
  opacity: 0.55;
}
.update-btn.spin svg {
  animation: updspin 0.9s linear infinite;
}
@keyframes updspin {
  to {
    transform: rotate(360deg);
  }
}
.dot {
  position: absolute;
  top: 3px;
  right: 3px;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent);
}
</style>
