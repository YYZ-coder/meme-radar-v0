import { defineConfig, devices } from "@playwright/test";

// Smoke tests run against this checkout's production build (`npm run build` first),
// served by Playwright on a dedicated port. Default mode uses the built-in mock API;
// RADAR_E2E_MODE=connected also starts the Go mock backend (../backend/bin/radar)
// and proxies /api/v1 to it through RADAR_API_ORIGIN.
const port = Number(process.env.RADAR_E2E_PORT ?? 31337);
const apiPort = Number(process.env.RADAR_E2E_API_PORT ?? 31437);
for (const [name, value] of [
  ["RADAR_E2E_PORT", port],
  ["RADAR_E2E_API_PORT", apiPort],
] as const) {
  if (!Number.isInteger(value) || value < 31000 || value > 31999) {
    throw new Error(`${name} must be an integer in 31000-31999`);
  }
}
const connected = process.env.RADAR_E2E_MODE === "connected";
const apiOrigin = `http://127.0.0.1:${apiPort}`;

const frontend = {
  command: `next start --hostname 127.0.0.1 --port ${port}`,
  url: `http://127.0.0.1:${port}/smart-money/inflow`,
  reuseExistingServer: false,
  timeout: 60_000,
  env: (connected ? { RADAR_API_ORIGIN: apiOrigin } : { RADAR_API_MOCK: "1" }) as Record<
    string,
    string
  >,
};

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: [["list"], ["html", { outputFolder: "playwright-report", open: "never" }]],
  outputDir: "test-results",

  use: {
    baseURL: `http://127.0.0.1:${port}`,
    // Default UI language is zh-CN; tests switch to English explicitly.
    locale: "zh-CN",
    trace: "on-first-retry",
  },

  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],

  webServer: connected
    ? [
        {
          command: `../backend/bin/radar api --store=mock --addr=127.0.0.1:${apiPort}`,
          url: `${apiOrigin}/healthz`,
          reuseExistingServer: false,
          timeout: 30_000,
          env: { RADAR_ADMIN_TOKEN: "mock-admin" },
        },
        frontend,
      ]
    : frontend,
});
