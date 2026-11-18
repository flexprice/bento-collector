import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath } from 'node:url'

// Builds into dist/, which the Go binary embeds (collector/server.go).
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  base: './',
  build: { outDir: 'dist', emptyOutDir: true, assetsDir: 'assets', chunkSizeWarningLimit: 900 },
  server: { proxy: { '/api': 'http://127.0.0.1:8090', '/healthz': 'http://127.0.0.1:8090' } },
})
