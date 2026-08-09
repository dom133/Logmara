import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    sourcemap: false,
    rollupOptions: {
      output: {
        // Vendor libs change far less often than app code - splitting them
        // out means a routine app deploy only invalidates the small app
        // chunk in browser cache, not these multi-hundred-KB dependencies.
        // Rolldown (Vite 8's bundler) requires manualChunks as a function,
        // unlike Rollup which also accepted a static object.
        manualChunks(id: string) {
          if (id.includes('node_modules')) {
            if (/[\\/]echarts(-for-react)?[\\/]/.test(id)) return 'vendor-echarts'
            if (/[\\/](react|react-dom|react-router|react-router-dom)[\\/]/.test(id)) return 'vendor-react'
            if (/[\\/](antd|@ant-design)[\\/]/.test(id)) return 'vendor-antd'
            if (/[\\/](i18next|react-i18next|i18next-browser-languagedetector)[\\/]/.test(id)) return 'vendor-i18n'
          }
        },
      },
    },
  },
  server: {
    host: '0.0.0.0',
    port: Number(process.env.PORT) || 3000,
    proxy: {
      '/api': 'http://localhost:8080'
    }
  }
})