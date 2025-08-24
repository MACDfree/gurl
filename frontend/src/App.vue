<script setup>
import { NButton, NInput, NSplit } from 'naive-ui'
import { ref } from 'vue'

const value = ref('')
const res = ref('')

const handleClick = async () => {
  res.value = await fetch('http://127.0.0.1:7777/api/request', {
    method: 'POST',
    body: value.value,
  }).then((res) => res.text())
}
</script>

<template>
  <div class="container">
    <div class="header"><n-button type="primary" @click="handleClick">执行</n-button></div>
    <div class="content">
      <n-split direction="vertical" style="height: 100%" :resize-trigger-size="5">
        <template #1>
          <div class="input-container">
            <n-input v-model:value="value" type="textarea" placeholder="" />
          </div>
        </template>
        <template #2>
          <div class="input-container">
            <n-input v-model:value="res" type="textarea" placeholder="" readonly />
          </div>
        </template>
      </n-split>
    </div>
  </div>
</template>

<style scoped>
.container {
  display: flex;
  flex-direction: column;
  height: 100vh;
  padding: 10px;
  box-sizing: border-box;
}

.header {
  flex: 0 0 auto;
  margin-bottom: 10px;
}

.content {
  flex: 1;
  min-height: 0;
}

.input-container {
  height: 100%;
  padding: 10px 0;
}

.input-container > .n-input--textarea {
  height: 100%;
}

.input-container > .n-input--textarea > :deep(.n-input-wrapper) {
  resize: none;
}
</style>
