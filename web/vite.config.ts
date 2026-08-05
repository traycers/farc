import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api/farcd': { target: 'http://localhost:8080', rewrite: (p) => p.replace(/^\/api\/farcd/, '') },
      '/api/hls': { target: 'http://localhost:8090', rewrite: (p) => p.replace(/^\/api\/hls/, '') },
      '/api/events': {
        target: 'http://localhost:8081',
        ws: true,
        rewrite: (p) => p.replace(/^\/api\/events/, ''),
      },
    },
  },
})
