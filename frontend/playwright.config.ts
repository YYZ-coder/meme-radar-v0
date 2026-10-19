import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: "html",

  use: {
    baseURL: "http://127.0.0.1:3107",
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

  // Run the current production build on an isolated port for smoke and E2E.
  // PLAYWRIGHT_NO_SERVER=1 is reserved for a manually managed service on that port.
  webServer:
    process.env.PLAYWRIGHT_NO_SERVER !== "1"
      ? {
          command: "npm run start -- --hostname 127.0.0.1 --port 3107",
          url: "http://127.0.0.1:3107",
          reuseExistingServer: false,
          timeout: 30_000,
        }
      : undefined,
});
