import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// 成果物は dist に出し、Go 側が go:embed で取り込む。
export default defineConfig({
  plugins: [svelte()],
  // 中身の掃除は scripts/clean-dist.mjs が行う。dist ごと消すと
  // go:embed の対象が無くなる。
  build: { outDir: 'dist', emptyOutDir: false },
  // 開発中は Vite の dev server から Go の API へ中継する。
  server: { proxy: { '/api': 'http://127.0.0.1:8317' } },
})
