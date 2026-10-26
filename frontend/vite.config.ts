/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      // Dev mode: backend serves WS + HTTP on :8080 (PROTOCOL.md §7).
      '/ws': { target: 'http://localhost:8080', ws: true },
      '/healthz': { target: 'http://localhost:8080' },
      '/metrics': { target: 'http://localhost:8080' },
      '/api': { target: 'http://localhost:8080' },
    },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
})
