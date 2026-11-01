// Playwright API and UI tests for the v1.0 demo path (TRD §9, NFR-10). The same tests run against
// local, CI or the server: SOCKABUS_API_URL is the Go API, SOCKABUS_WEB_URL the console.
// The backend must run with SOCKABUS_LLM_PROVIDER=fake and a short reply delay (see tests/README.md).
import { defineConfig, devices } from "@playwright/test";

const ci = !!process.env.CI;

export default defineConfig({
  testDir: "tests",
  timeout: 120_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1, // one shared database; tests clean up after themselves
  retries: ci ? 1 : 0,
  forbidOnly: ci,
  reporter: ci ? [["list"], ["html", { open: "never" }]] : [["list"]],
  use: { trace: "retain-on-failure" },
  projects: [
    {
      name: "api",
      testDir: "tests/api",
      use: { baseURL: process.env.SOCKABUS_API_URL ?? "http://localhost:8080" },
    },
    {
      name: "ui",
      testDir: "tests/ui",
      use: { ...devices["Desktop Chrome"], baseURL: process.env.SOCKABUS_WEB_URL ?? "http://localhost:3000" },
    },
  ],
});
