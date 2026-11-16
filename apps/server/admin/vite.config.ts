import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  base: '/admin/',
  plugins: [vue(), tailwindcss()],
  build: { sourcemap: false },
  // Use only for local UI work; production is always served by the TLS server.
  server: { host: '127.0.0.1', port: 5174, strictPort: true },
});
