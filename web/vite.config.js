import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  // 绝对路径：相对路径（'./'）在 /device/:id 深链接刷新时会解析成 /device/assets/* 导致 404 空白
  base: '/',
  server: {
    port: 5173,
    proxy: {
      // 后端 Master：开发环境代理 REST 与 WebSocket
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
        ws: true,
      },
    },
  },
  build: {
    // 图表库按需分包，保证首屏（开发文档第 8 节）
    chunkSizeWarningLimit: 900,
  },
})
