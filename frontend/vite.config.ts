import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    sourcemap: false,
    // No manualChunks: this app has real circular dependencies between
    // node_modules code and shared app modules (e.g. src/services/api.ts).
    // Any hand-rolled chunk grouping - whether split per-package
    // (vendor-react/vendor-antd/...) or a single lumped vendor bucket -
    // ends up putting two mutually-importing chunks in an order where one
    // reads a binding from the other before it's initialized. That shows
    // up in production builds only (vite dev never applies manualChunks)
    // as either "Minified React error #130: Element type is invalid ...
    // got: object" or a raw "<x> is not a function" crash. Rollup's
    // automatic chunking already accounts for the real dependency graph
    // and does not have this failure mode, so let it decide.
  },
  server: {
    host: '0.0.0.0',
    port: Number(process.env.PORT) || 3000,
    proxy: {
      '/api': 'http://localhost:8080'
    }
  }
})