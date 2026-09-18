import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

// dev 下后端端口与 token 由 Makefile 注入（make dev）。token 由代理加到 /api 请求上，
// 浏览器侧完全不需要知道它——这样 dev 与生产的中间件行为一致，不必给 dev 开后门。
const apiPort = process.env.GURL_API_PORT || "9246";
const apiToken = process.env.GURL_TOKEN || "dev-local";

export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.VITE_PORT) || 9245,
    strictPort: true,
    proxy: {
      "/api": {
        target: `http://127.0.0.1:${apiPort}`,
        changeOrigin: false,
        headers: { authorization: `Bearer ${apiToken}` },
      },
    },
  },
  plugins: [vue()],
  build: {
    rollupOptions: {
      output: {
        // 切成独立 chunk：改一行业务代码不该让编辑器/组件库的缓存失效。
        // 回环上「下载」就是内存拷贝，这里要的是缓存粒度而不是体积。
        manualChunks(id) {
          if (id.includes("node_modules/naive-ui") || id.includes("node_modules/css-render")) return "naive";
          if (id.includes("node_modules/@codemirror") || id.includes("node_modules/@lezer")) return "editor";
        },
      },
    },
  },
});
