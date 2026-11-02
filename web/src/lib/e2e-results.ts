// Playwright results for /automate (server only: imported by the /api/automate routes, never by the browser).
// Reads the JSON reporter output written by playwright.config.ts and archived by scripts/e2e-monitor.sh:
//   <results>/latest.json             the last run (any `playwright test`, manual or monitor)
//   <results>/history/<UTC ts>.json   one file per monitor run (last 50)
//   <results>/history/<UTC ts>.files/ that run's screenshots (copied out of test-results/)
// <results> is SOCKABUS_E2E_RESULTS_DIR, default <repo>/playwright/results (web/ is the working directory under
// `pnpm dev`). The HTML report and the live test-results/ sit one level up from it, in playwright/;
// SOCKABUS_E2E_REPORT_DIR / SOCKABUS_E2E_ARTIFACTS_DIR override them.
import { readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";
import type { E2EResult, E2ERun, E2ERunSummary, E2EStep, E2ETest } from "./e2e-types";

export const resultsDir = () => path.resolve(process.env.SOCKABUS_E2E_RESULTS_DIR || path.join(process.cwd(), "..", "playwright", "results"));
export const reportDir = () => path.resolve(process.env.SOCKABUS_E2E_REPORT_DIR || path.join(resultsDir(), "..", "playwright-report"));
export const artifactsDir = () => path.resolve(process.env.SOCKABUS_E2E_ARTIFACTS_DIR || path.join(resultsDir(), "..", "test-results"));
const historyDir = () => path.join(resultsDir(), "history");

// Run ids: "latest" or a history file name without .json (20261101T085012Z). Nothing else reaches the disk.
const HISTORY_ID = /^\d{8}T\d{6}Z$/;
export const validRunId = (id: string) => id === "latest" || HISTORY_ID.test(id);

const runFile = (id: string) => (id === "latest" ? path.join(resultsDir(), "latest.json") : path.join(historyDir(), `${id}.json`));

// Playwright's JSON reporter (only the fields we read).
type PwStep = { title: string; duration: number; error?: { message?: string }; steps?: PwStep[] };
type PwAttachment = { name: string; contentType: string; path?: string };
type PwResult = { status: string; duration: number; retry: number; errors?: { message?: string }[]; steps?: PwStep[]; attachments?: PwAttachment[] };
type PwTest = { projectName: string; status: string; results: PwResult[] };
type PwSpec = { title: string; file: string; line: number; tests: PwTest[] };
type PwSuite = { title: string; file: string; specs?: PwSpec[]; suites?: PwSuite[] };
type PwReport = {
  config?: { metadata?: Record<string, unknown> };
  suites?: PwSuite[];
  errors?: { message?: string }[];
  stats?: { startTime: string; duration: number; expected: number; unexpected: number; skipped: number; flaky: number };
};

const ANSI = /\u001b\[[0-9;]*m/g;
const clean = (s: string | undefined) => (s ?? "").replace(ANSI, "").trim();

async function load(id: string): Promise<PwReport | null> {
  try {
    return JSON.parse(await readFile(runFile(id), "utf8")) as PwReport;
  } catch {
    return null; // missing or half-written: treated as "no run"
  }
}

function summarize(id: string, r: PwReport): E2ERunSummary | null {
  const s = r.stats;
  if (!s?.startTime) return null;
  const failed = s.unexpected + (r.errors?.length ? 1 : 0);
  return {
    id,
    startedAt: s.startTime,
    durationMs: Math.round(s.duration),
    llm: typeof r.config?.metadata?.llm === "string" ? r.config.metadata.llm : "unknown",
    passed: s.expected,
    failed: s.unexpected,
    skipped: s.skipped,
    flaky: s.flaky,
    status: failed > 0 ? "failed" : s.expected + s.flaky === 0 ? "empty" : "passed",
  };
}

const flatSteps = (steps: PwStep[] | undefined, depth = 0): E2EStep[] =>
  (steps ?? []).flatMap((st) => [
    { title: st.title, depth, durationMs: Math.round(st.duration), status: st.error ? ("failed" as const) : ("passed" as const), error: st.error ? clean(st.error.message) : undefined },
    ...flatSteps(st.steps, depth + 1),
  ]);

// test.status: expected | unexpected | flaky | skipped; the last result says how the final attempt ended.
function testStatus(t: PwTest, last: PwResult | undefined): E2ETest["status"] {
  if (t.status === "flaky") return "flaky";
  if (t.status === "skipped" || last?.status === "skipped") return "skipped";
  if (t.status === "expected") return "passed";
  return last?.status === "timedOut" ? "timedOut" : last?.status === "interrupted" ? "interrupted" : "failed";
}

// An attachment path is absolute on the machine that ran the suite; keep the part under test-results/.
const artifactRel = (p: string | undefined) => {
  const i = p?.replace(/\\/g, "/").lastIndexOf("/test-results/") ?? -1;
  return i >= 0 ? p!.replace(/\\/g, "/").slice(i + "/test-results/".length) : null;
};

function tests(id: string, suites: PwSuite[] | undefined, out: E2ETest[] = []): E2ETest[] {
  for (const su of suites ?? []) {
    for (const sp of su.specs ?? []) {
      for (const t of sp.tests) {
        const last = t.results.at(-1);
        const status = testStatus(t, last);
        const results: E2EResult[] = t.results.map((r) => ({ retry: r.retry, status: r.status, durationMs: Math.round(r.duration) }));
        const screenshots = t.results
          .flatMap((r) => r.attachments ?? [])
          .filter((a) => a.contentType === "image/png")
          .map((a) => artifactRel(a.path))
          .filter((rel): rel is string => !!rel)
          .map((rel) => ({ name: path.basename(rel), url: `/api/automate/artifact?run=${encodeURIComponent(id)}&path=${encodeURIComponent(rel)}` }));
        out.push({
          project: t.projectName,
          file: sp.file,
          line: sp.line,
          title: sp.title,
          status,
          durationMs: t.results.reduce((a, r) => a + r.duration, 0) | 0,
          error: status === "passed" || status === "skipped" ? undefined : clean(last?.errors?.map((e) => e.message).join("\n\n")) || undefined,
          steps: flatSteps(last?.steps),
          results,
          screenshots,
        });
      }
    }
    tests(id, su.suites, out);
  }
  return out;
}

export async function listRuns(): Promise<E2ERunSummary[]> {
  const ids: string[] = [];
  try {
    for (const f of await readdir(historyDir())) if (f.endsWith(".json") && HISTORY_ID.test(f.slice(0, -5))) ids.push(f.slice(0, -5));
  } catch {
    // no history yet
  }
  ids.sort().reverse();
  const runs = (await Promise.all(ids.map(async (id) => {
    const r = await load(id);
    return r ? summarize(id, r) : null;
  }))).filter((r): r is E2ERunSummary => !!r);
  return runs;
}

export async function latestRun(): Promise<E2ERunSummary | null> {
  const r = await load("latest");
  return r ? summarize("latest", r) : null;
}

export async function getRun(id: string): Promise<E2ERun | null> {
  if (!validRunId(id)) return null;
  const r = await load(id);
  const sum = r && summarize(id, r);
  if (!r || !sum) return null;
  const meta = r.config?.metadata ?? {};
  return {
    ...sum,
    api: typeof meta.api === "string" ? meta.api : undefined,
    web: typeof meta.web === "string" ? meta.web : undefined,
    globalErrors: (r.errors ?? []).map((e) => clean(e.message)).filter(Boolean),
    tests: tests(id, r.suites),
  };
}

// Where a run's screenshot lives: history runs keep a copy next to their JSON; "latest" reads test-results/.
export function artifactPath(id: string, rel: string): string | null {
  if (!validRunId(id) || !rel.endsWith(".png")) return null;
  const base = id === "latest" ? artifactsDir() : path.join(historyDir(), `${id}.files`);
  return inside(base, rel);
}

// base/rel, or null when rel climbs out of base.
export function inside(base: string, rel: string): string | null {
  const p = path.resolve(base, rel);
  return p.startsWith(base + path.sep) ? p : null;
}

export async function isFile(p: string): Promise<boolean> {
  try {
    return (await stat(p)).isFile();
  } catch {
    return false;
  }
}
