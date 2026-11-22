import { defineConfig, devices } from "@playwright/test";

// End-to-end tests run against a live stack: the Go API (with SEED_DEMO=true) and
// this console. By default Playwright starts the production build of the console;
// set E2E_BASE_URL to test an already running instance (e.g. docker compose on :3000).
const baseURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:3100";

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  fullyParallel: false,
  retries: 0,
  reporter: [["list"]],
  use: { baseURL, trace: "retain-on-failure", ...devices["Desktop Chrome"] },
  webServer: process.env.E2E_BASE_URL
    ? undefined
    : {
        command: "node .next/standalone/server.js",
        url: `${baseURL}/login`,
        reuseExistingServer: true,
        env: { PORT: "3100", HOSTNAME: "127.0.0.1", BACKEND_URL: process.env.BACKEND_URL ?? "http://localhost:8080" },
      },
});
