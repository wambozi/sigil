/// <reference types="vitest" />
import { defineConfig } from 'vite'
import preact from '@preact/preset-vite'

export default defineConfig({
  plugins: [preact()],
  build: { outDir: 'dist' },
  test: {
    environment: 'jsdom',
    globals: true,
  },
})
