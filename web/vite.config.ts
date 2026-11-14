import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    // straight into the Go package that embeds it
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
  server: {
    // for `npm run dev` next to a running `rehearse --port 7777 ...`
    proxy: { '/api': 'http://localhost:7777' },
  },
})
