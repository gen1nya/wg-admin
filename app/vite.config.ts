import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import tailwindcss from '@tailwindcss/vite';

const API_PROXY = process.env.API_PROXY || 'http://127.0.0.1:3000';

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api':  { target: API_PROXY, changeOrigin: false },
      '/auth': { target: API_PROXY, changeOrigin: false },
    },
  },
  build: {
    outDir: 'dist/web',
    emptyOutDir: true,
    sourcemap: true,
  },
});
