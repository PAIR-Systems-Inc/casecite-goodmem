import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

// Tests never load office.js: the Office and Word globals are stubbed per
// test (see src/test/setup.ts), so the config does not reuse vite.config.ts
// and its HTTPS dev-server setup.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
