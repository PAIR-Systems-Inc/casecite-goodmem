import { defineConfig, devices } from '@playwright/test';

// CI uses the Vite default port. Locally, set PLAYWRIGHT_PORT when 3000 is
// taken (the dev server and the tests both follow it).
const PORT = process.env.PLAYWRIGHT_PORT || '3000';
const EXTERNAL_URL = process.env.E2E_BASE_URL;
const BASE_URL = EXTERNAL_URL || `http://localhost:${PORT}`;

export default defineConfig({
  // Keep upstream's mocked UI suite separate from tests of the real Go API.
  testDir: EXTERNAL_URL ? './e2e-real' : './e2e',
  outputDir: EXTERNAL_URL ? './test-results/real' : './test-results/ui',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI || EXTERNAL_URL ? 1 : undefined,
  reporter: 'html',

  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'], launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE } : undefined },
    },
    {
      name: 'firefox',
      use: { ...devices['Desktop Firefox'] },
    },
  ],

  webServer: EXTERNAL_URL ? undefined : {
    command: `npm run dev -- --port ${PORT} --strictPort`,
    url: BASE_URL,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
