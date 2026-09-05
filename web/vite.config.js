import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import VueI18nPlugin from '@intlify/unplugin-vue-i18n/vite'
import path from 'node:path'

export default defineConfig({
  plugins: [
    vue(),
    // 构建期预编译 locale 消息为函数，运行时不再依赖 eval（管理后台 CSP 禁止 unsafe-eval）
    VueI18nPlugin({
      include: [path.resolve(__dirname, './src/locales/zh-CN.js'), path.resolve(__dirname, './src/locales/en-US.js')]
    })
  ],
  define: {
    // 启用 JIT 编译：预编译的 AST 消息在运行时不经 eval 直接组合成函数
    __INTLIFY_JIT_COMPILATION__: true
  },
  resolve: {
    alias: {
      // runtime-only 构建不含消息编译器，配合上方预编译使用
      'vue-i18n': 'vue-i18n/dist/vue-i18n.runtime.esm-bundler.js'
    }
  },
  test: { environment: 'jsdom' },
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:16601',
        changeOrigin: true
      }
    }
  },
  build: {
    outDir: '../internal/adminweb/dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
		chunkFileNames(chunk) {
			// Go embed skips files whose basename begins with underscore when a
			// directory is embedded. Vite's helper chunk otherwise breaks production.
			// 非入口的共享 chunk 若也名为 index 会与入口混淆，统一改名 shared。
			const name = chunk.name.replace(/^_+/, '')
			const base = name === 'index' && !chunk.isEntry ? 'shared' : name
			return `assets/${base}-[hash].js`
		},
        manualChunks(id) {
          if (id.includes('/src/locales/')) return 'locales'
          if (id.includes('/node_modules/@element-plus/icons-vue/')) return 'icons'
          if (id.includes('/node_modules/vue/') || id.includes('/node_modules/@vue/') || id.includes('/node_modules/pinia/') || id.includes('/node_modules/vue-router/')) return 'vue'
          if (id.includes('/node_modules/axios/')) return 'axios'
        }
      }
    }
  }
})
