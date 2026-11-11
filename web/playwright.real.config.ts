import { defineConfig } from '@playwright/test';

// Runs against a real `waves serve` started by the Go end-to-end test
// (internal/e2e TestDashboardOnTheRealService), which passes the address.
export default defineConfig({
  testDir: 'e2e-real',
  timeout: 30_000,
  workers: 1,
  reporter: [['list']],
  use: { baseURL: process.env.WAVES_URL, trace: 'off' },
});
