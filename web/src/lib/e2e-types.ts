// Shapes the /api/automate routes return (src/lib/e2e-results.ts builds them from Playwright's JSON).
export type E2ERunSummary = {
  id: string; // "latest" or a history id (UTC timestamp)
  startedAt: string;
  durationMs: number;
  llm: string; // config.metadata.llm: the persona LLM the test stack ran
  passed: number;
  failed: number;
  skipped: number;
  flaky: number;
  status: "passed" | "failed" | "empty";
};

export type E2EStep = { title: string; depth: number; durationMs: number; status: "passed" | "failed"; error?: string };
export type E2EResult = { retry: number; status: string; durationMs: number };

export type E2ETest = {
  project: string;
  file: string;
  line: number;
  title: string;
  status: "passed" | "failed" | "timedOut" | "interrupted" | "skipped" | "flaky";
  durationMs: number;
  error?: string;
  steps: E2EStep[];
  results: E2EResult[];
  screenshots: { name: string; url: string }[];
};

export type E2ERun = E2ERunSummary & { api?: string; web?: string; globalErrors: string[]; tests: E2ETest[] };

export type E2ERunsResponse = { dir: string; latest: E2ERunSummary | null; history: E2ERunSummary[]; report: boolean };
