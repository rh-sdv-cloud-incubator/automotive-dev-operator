import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath, URL } from 'node:url';

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/v1': {
        // For dev: leave VITE_API_BASE unset in the UI so requests hit /v1 and get proxied here.
        // Point this target at either a local API or your cluster route.
        target: process.env.VITE_API_ROUTE || process.env.VITE_API_BASE || 'http://localhost:8080',
        changeOrigin: true,
        secure: false,
        rewrite: (p) => p,
      },
    },
  },
});


