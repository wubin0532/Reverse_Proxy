import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
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
			const name = chunk.name.replace(/^_+/, '')
			return `assets/${name}-[hash].js`
		},
        manualChunks(id) {
          if (id.includes('/node_modules/@element-plus/icons-vue/')) return 'icons'
          if (id.includes('/node_modules/vue/') || id.includes('/node_modules/@vue/') || id.includes('/node_modules/pinia/') || id.includes('/node_modules/vue-router/')) return 'vue'
          if (id.includes('/node_modules/axios/')) return 'axios'
        }
      }
    }
  }
})
