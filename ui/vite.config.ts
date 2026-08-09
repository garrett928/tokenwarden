import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Dev-server proxy for /api avoids CORS entirely: the browser only ever
// talks to this origin, Vite forwards /api/* to the daemon server-side.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.TOKENWARDEN_API_PROXY_TARGET ?? 'http://127.0.0.1:7842',
        changeOrigin: false,
      },
    },
  },
})
