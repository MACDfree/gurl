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
});
