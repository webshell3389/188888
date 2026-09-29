import { fileURLToPath, URL } from 'node:url'
import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import AutoImport from 'unplugin-auto-import/vite'
import Components from 'unplugin-vue-components/vite'
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers'

// 后端地址：默认 127.0.0.1:9001（Go mockc2 主后端 / Python FastAPI 同端口）
// 覆盖方式：frontend/.env.local 写 VITE_PROXY_TARGET=http://192.168.36.253:9001
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, fileURLToPath(new URL('.', import.meta.url)), '')
  const target = env.VITE_PROXY_TARGET || 'http://127.0.0.1:9001'

  // 运维面只读接口前缀（契约红线：不代理 /beacon /war /p /result /event /u /nb /ph /a 等回传端点）
  // /api/* 只代理"入库解析后的业务实体"子路径，不含交付链的 /api/ip-sync/sync
  const proxy: Record<string, { target: string; changeOrigin: boolean; secure: boolean; ws: boolean }> = {}
  for (const prefix of [
    '/__dash',
    '/__q',
    '/__auth',
    '/__control',
    '/panel',
    '/__stats',
    '/api/data',
    '/api/file',
    '/api/activity',
    '/api/stats',
    '/api/list',
    '/api/device',
    '/api/tasks',
  ]) {
    proxy[prefix] = { target, changeOrigin: true, secure: false, ws: false }
  }

  return {
    // 相对基址 + hash 路由：产物可直接挂进任意静态目录，无需后端改路由
    base: './',
    plugins: [
      vue(),
      AutoImport({
        imports: ['vue', 'vue-router', 'pinia'],
        resolvers: [ElementPlusResolver()],
        dts: 'src/types/auto-imports.d.ts',
        eslintrc: { enabled: false },
      }),
      Components({
        resolvers: [ElementPlusResolver()],
        dts: 'src/types/components.d.ts',
        dirs: ['src/components'],
        deep: true,
      }),
    ],
    resolve: {
      alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
    },
    css: {
      preprocessorOptions: {
        scss: { api: 'modern-compiler' },
      },
    },
    server: {
      port: 5173,
      host: '127.0.0.1',
      proxy,
    },
    build: {
      target: 'es2020',
      outDir: 'dist',
      assetsDir: 'assets',
      chunkSizeWarningLimit: 1600,
      rollupOptions: {
        output: {
          manualChunks: {
            vendor: ['vue', 'vue-router', 'pinia'],
            ui: ['element-plus', '@element-plus/icons-vue'],
            charts: ['echarts'],
          },
        },
      },
    },
  }
})
