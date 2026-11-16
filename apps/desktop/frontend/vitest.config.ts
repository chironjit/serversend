import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';

export default defineConfig({
  // Leave template asset URLs as plain attributes: importing public-directory
  // paths such as /wails.png fails under Vitest on Windows (no drive letter).
  plugins: [vue({ template: { transformAssetUrls: false } })],
  test: {
    environment: 'happy-dom',
    include: ['src/**/*.test.ts'],
    restoreMocks: true,
  },
});
