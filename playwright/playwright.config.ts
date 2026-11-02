// Playwright API and UI tests (TRD §9, NFR-10). The same tests run against local, CI or the server:
// SOCKABUS_API_URL is the Go API, SOCKABUS_WEB_URL the console. Locally, start the throwaway stack with
// scripts/e2e-stack.sh up and run scripts/e2e-stack.sh test (playwright/README.md). Never point it at Supabase.
import { defineConfig, devices } from "@playwright/test";

const ci = !!process.env.CI;

// The suite writes append-only rows (messages, sightings, verifications): refuse production.
for (const k of ["DATABASE_URL", "SOCKABUS_POSTGRES_URL"]) {
  if ((process.env[k] ?? "").includes("supabase")) throw new Error(`${k} points at Supabase: refusing to run the e2e suite (use scripts/e2e-stack.sh)`);
}

export default defineConfig({
  testDir: ".", // paths here are relative to this file (playwright/)
  timeout: 180_000, // real LLM replies take seconds each
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1, // one shared database; tests clean up after themselves
  retries: ci ? 1 : 0,
  forbidOnly: true,
  reporter: [
    ["list"],
    ["html", { open: "never", outputFolder: "playwright-report" }],
    ["json", { outputFile: "results/latest.json" }],
  ],
  outputDir: "test-results",
  // Shown in the HTML report: which persona LLM the backend ran (scripts/e2e-stack.sh sets it).
  metadata: { llm: process.env.SOCKABUS_E2E_LLM ?? "unknown", api: process.env.SOCKABUS_API_URL, web: process.env.SOCKABUS_WEB_URL },
  use: { trace: "retain-on-failure" },
  projects: [
    {
      name: "api",
      testDir: "api",
      use: { baseURL: process.env.SOCKABUS_API_URL ?? "http://localhost:8080" },
    },
    {
      name: "ui",
      testDir: "ui",
      use: {
        ...devices["Desktop Chrome"],
        baseURL: process.env.SOCKABUS_WEB_URL ?? "http://localhost:3000",
        screenshot: "on",
        trace: "on",
      },
    },
  ],
});
