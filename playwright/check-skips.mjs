// Skip check for the Sockabus Playwright suite.
// Reads Playwright's own results (results/latest.json, written by every run) and reports, per test,
// whether it actually ran. Also lists tests that exist but were filtered out of the run (e.g. @live),
// and any skip/fixme calls in the spec files.
//
//   cd ~/Sockabus/playwright
//   make test-ci                                    # writes results/latest.json
//   node check-skips.mjs > results/skip-check.txt   # report to a file
//   cat results/skip-check.txt
//
// Exit code 1 if any test was skipped, failed or is marked fixme; 0 if every test that ran passed.
import { execSync } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

const file = process.argv[2] ?? "results/latest.json";
const report = JSON.parse(readFileSync(file, "utf8"));

// Walk the nested suites and collect one row per test.
const rows = [];
const walk = (suite, fileName) => {
  for (const spec of suite.specs ?? []) {
    for (const t of spec.tests ?? []) {
      const last = t.results?.at(-1);
      rows.push({
        file: spec.file ?? fileName,
        title: spec.title,
        project: t.projectName,
        outcome: t.status, // expected | unexpected | flaky | skipped
        result: last?.status ?? "not run", // passed | failed | timedOut | skipped | interrupted
        ms: last?.duration ?? 0,
        steps: (last?.steps ?? []).length,
        notes: (t.annotations ?? []).map((a) => a.type + (a.description ? `: ${a.description}` : "")).join("; "),
      });
    }
  }
  for (const child of suite.suites ?? []) walk(child, suite.file ?? fileName);
};
for (const s of report.suites ?? []) walk(s, s.file);

const count = (pred) => rows.filter(pred).length;
const passed = count((r) => r.result === "passed");
const skipped = rows.filter((r) => r.result === "skipped" || r.outcome === "skipped");
const failed = rows.filter((r) => r.outcome === "unexpected" || r.outcome === "flaky");
const s = report.stats ?? {};

const out = [];
const line = (x = "") => out.push(x);
line(`Skip check · ${new Date(s.startTime ?? Date.now()).toISOString()} · ${file}`);
line(`LLM: ${report.config?.metadata?.llm ?? "unknown"} · API: ${report.config?.metadata?.api ?? "?"}`);
line();
line(`Tests in this run : ${rows.length}`);
line(`Passed            : ${passed}`);
line(`Skipped           : ${skipped.length}${skipped.length ? "   <-- check these" : ""}`);
line(`Failed / flaky    : ${failed.length}${failed.length ? "   <-- check these" : ""}`);
line(`Playwright stats  : expected=${s.expected} skipped=${s.skipped} unexpected=${s.unexpected} flaky=${s.flaky}`);
line();

// Tests that exist in the suite but were not part of this run (e.g. filtered by --grep-invert @live).
let filtered = [];
try {
  const all = JSON.parse(execSync("npx playwright test --list --reporter=json", { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }));
  const listed = [];
  const walkList = (suite, fileName) => {
    for (const spec of suite.specs ?? []) for (const t of spec.tests ?? []) listed.push({ file: spec.file ?? fileName, title: spec.title, project: t.projectName, tags: spec.tags ?? [] });
    for (const c of suite.suites ?? []) walkList(c, suite.file ?? fileName);
  };
  for (const su of all.suites ?? []) walkList(su, su.file);
  const ran = new Set(rows.map((r) => `${r.project}|${r.file}|${r.title}`));
  filtered = listed.filter((t) => !ran.has(`${t.project}|${t.file}|${t.title}`));
  line(`Tests in the whole suite: ${listed.length} · not in this run: ${filtered.length}`);
  for (const t of filtered) line(`  NOT RUN  [${t.project}] ${t.file} › ${t.title}${t.tags.length ? "  " + t.tags.map((x) => (x.startsWith("@") ? x : "@" + x)).join(" ") : ""}`);
  if (!filtered.length) line("  (every test in the suite was part of this run)");
} catch {
  line("Could not list the whole suite (run this from playwright/ with the stack env loaded).");
}
line();

// skip / fixme / only written into the spec files.
const specFiles = ["api", "ui"].flatMap((d) => readdirSync(d).filter((f) => f.endsWith(".ts")).map((f) => join(d, f)));
const marks = [];
for (const f of specFiles) {
  readFileSync(f, "utf8").split("\n").forEach((txt, i) => {
    if (/\btest\.(skip|fixme|only|fail)\s*\(|testInfo\.(skip|fixme)\s*\(|\.skip\s*\(/.test(txt)) marks.push(`  ${f}:${i + 1}  ${txt.trim()}`);
  });
}
line(`skip / fixme / only calls in spec files: ${marks.length}`);
for (const m of marks) line(m);
if (!marks.length) line("  (none)");
line();

line("Per test (result · duration · steps run):");
for (const r of rows) {
  const flag = r.result === "passed" ? "PASS" : r.result.toUpperCase();
  line(`  ${flag.padEnd(7)} ${String(r.ms).padStart(6)}ms  ${String(r.steps).padStart(2)} steps  [${r.project}] ${r.file} › ${r.title}${r.notes ? "  (" + r.notes + ")" : ""}`);
}
line();
const ok = !skipped.length && !failed.length && !marks.some((m) => /fixme|only/.test(m));
line(ok ? `RESULT: OK — all ${passed} tests in this run executed and passed; ${filtered.length} filtered out on purpose (listed above).` : "RESULT: CHECK — see the flagged tests above.");

console.log(out.join("\n"));
process.exit(ok ? 0 : 1);
