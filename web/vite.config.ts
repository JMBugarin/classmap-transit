import { defineConfig } from 'vitest/config'
export default defineConfig({
  server: { proxy: { '/api': { target: 'http://127.0.0.1:8080', rewrite: p => p.replace(/^\/api/, '') } } },
  test: { include: ['src/**/*.test.ts'] },
})
