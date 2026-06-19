import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import path from 'path'

const serverWebDir = path.resolve(__dirname, '../server/cmd/server/web')

export default defineConfig({
  plugins: [svelte()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080'
    }
  },
  build: {
    outDir: serverWebDir,
    sourcemap: false,
    copyPublicDir: false,
    emptyOutDir: true
  }
})
