/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
  },
  server: {
    proxy: {
      '/api/farcd': {
        target: 'http://localhost:8080',
        ws: true,
        rewrite: (p) => p.replace(/^\/api\/farcd/, ''),
      },
      '/api/hls': { target: 'http://localhost:8090', rewrite: (p) => p.replace(/^\/api\/hls/, '') },
      '/api/apid': { target: 'http://localhost:8100', rewrite: (p) => p.replace(/^\/api\/apid/, '') },
      '/api/whep': {
        target: 'http://localhost:8889',
        rewrite: (p) => p.replace(/^\/api\/whep/, ''),
        // Dev-mode equivalent of nginx's proxy_redirect (web/nginx.conf) --
        // mediamtx's WHEP Location response header is path-absolute with no
        // prefix (.scratch/live-page-fixes/issues/03-whep-proxy-through-nginx.md),
        // so without this rewrite the browser's teardown DELETE would
        // escape the /api/whep prefix and silently fail against the dev
        // server, leaking the WHEP session on mediamtx.
        configure: (proxy) => {
          proxy.on('proxyRes', (proxyRes) => {
            const location = proxyRes.headers.location
            if (typeof location === 'string' && location.startsWith('/')) {
              proxyRes.headers.location = `/api/whep${location}`
            }
          })
        },
      },
      '/api/events': {
        target: 'http://localhost:8081',
        ws: true,
        rewrite: (p) => p.replace(/^\/api\/events/, ''),
      },
    },
  },
})
