"use client";
// Playwright results (playwright/README.md "Monitoring"): the latest run, every test with its steps, and the
// monitor's run history. Data comes from /api/automate/* (src/lib/e2e-results.ts), read from disk on the
// server. Real results only: with nothing on disk the page says so.
import { Fragment, useCallback, useEffect, useState } from "react";
import { Badge, Head, Loading, type Tone } from "@/components/ui";
import { plural } from "@/lib/format";
import type { E2ERun, E2ERunsResponse, E2ERunSummary, E2ETest } from "@/lib/e2e-types";

const REPORT = "/api/automate/report/index.html";
const POLL_MS = 30_000;

const TONE: Record<string, Tone> = { passed: "success", failed: "danger", timedOut: "danger", interrupted: "danger", skipped: "neutral", flaky: "warning", empty: "neutral" };
const WORD: Record<string, string> = { timedOut: "timed out", empty: "no tests" };
const StatusBadge = ({ s }: { s: string }) => <Badge word={WORD[s] ?? s} tone={TONE[s] ?? "neutral"} />;

const dur = (ms: number) => {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  const m = Math.floor(ms / 60_000);
  return `${m} min ${Math.round((ms % 60_000) / 1000)} s`;
};
const when = (iso: string) =>
  new Date(iso).toLocaleString("en-GB", { day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" });
const runLabel = (r: E2ERunSummary) => (r.id === "latest" ? "Latest run" : `Monitor run ${r.id}`);

type Filter = "all" | "failed" | "passed";

export function AutomateView() {
  const [runs, setRuns] = useState<E2ERunsResponse | null>(null);
  const [listErr, setListErr] = useState<string | null>(null);
  const [sel, setSel] = useState<string | null>(null);
  const [run, setRun] = useState<E2ERun | null>(null);
  const [runErr, setRunErr] = useState<string | null>(null);

  const loadList = useCallback(async () => {
    try {
      const r = await fetch("/api/automate/runs", { cache: "no-store" });
      if (!r.ok) throw new Error(`GET /api/automate/runs answered ${r.status}`);
      setRuns((await r.json()) as E2ERunsResponse);
      setListErr(null);
    } catch (e) {
      setListErr(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    const first = setTimeout(() => void loadList(), 0);
    const t = setInterval(() => void loadList(), POLL_MS);
    return () => {
      clearTimeout(first);
      clearInterval(t);
    };
  }, [loadList]);

  // Default selection: the latest run, else the newest monitor run.
  const current = sel ?? (runs?.latest ? "latest" : (runs?.history[0]?.id ?? null));
  const stamp = current === "latest" ? runs?.latest?.startedAt : runs?.history.find((h) => h.id === current)?.startedAt;

  useEffect(() => {
    if (!current) return;
    let live = true;
    fetch(`/api/automate/runs/${encodeURIComponent(current)}`, { cache: "no-store" })
      .then(async (r) => {
        if (!r.ok) throw new Error(((await r.json().catch(() => null)) as { error?: string } | null)?.error ?? `HTTP ${r.status}`);
        return (await r.json()) as E2ERun;
      })
      .then((j) => live && (setRun(j), setRunErr(null)))
      .catch((e: unknown) => live && setRunErr(e instanceof Error ? e.message : String(e)));
    return () => {
      live = false;
    };
    // stamp: refetch when latest.json is replaced by a newer run
  }, [current, stamp]);

  const head = (
    <Head
      title="Automate"
      sub="Playwright API and UI suite against the throwaway test stack: the latest run, every test and step, and the hourly monitor's history."
      right={
        <div className="row-gap">
          <button className="btn btn-secondary btn-sm" type="button" onClick={() => void loadList()} data-testid="automate-refresh">
            Refresh
          </button>
          {runs?.report ? (
            <a className="btn btn-primary btn-sm" href={REPORT} target="_blank" rel="noreferrer" data-testid="automate-report-link">
              Open HTML report ↗
            </a>
          ) : null}
        </div>
      }
    />
  );

  if (listErr && !runs)
    return (
      <>
        {head}
        <div className="banner err" role="alert">
          <div className="txt">
            <h3>Can&apos;t read the test results</h3>
            <p>{listErr}</p>
          </div>
        </div>
      </>
    );
  if (!runs) return <Loading />;
  if (!runs.latest && runs.history.length === 0)
    return (
      <>
        {head}
        <div className="card empty" data-testid="automate-empty">
          <b>No Playwright run on disk yet</b>
          <span>
            Nothing in <span className="mono">{runs.dir}</span>. Run the suite once with <span className="mono">make monitor</span> (or{" "}
            <span className="mono">scripts/e2e-stack.sh up && scripts/e2e-stack.sh test</span>) and this page shows it.
          </span>
        </div>
      </>
    );

  return (
    <>
      {head}
      {runErr && (
        <div className="banner err" role="alert">
          <div className="txt">
            <h3>Can&apos;t load this run</h3>
            <p>{runErr}</p>
          </div>
        </div>
      )}
      {run && run.id === current ? <RunView run={run} reportOn={runs.report} /> : !runErr ? <Loading /> : null}
      <History runs={runs} current={current} pick={setSel} />
    </>
  );
}

function RunView({ run, reportOn }: { run: E2ERun; reportOn: boolean }) {
  const [filter, setFilter] = useState<Filter>("all");
  const ok = run.status === "passed";
  const bad = run.tests.filter((t) => t.status !== "passed" && t.status !== "skipped" && t.status !== "flaky");
  const shown = run.tests.filter((t) => (filter === "all" ? true : filter === "failed" ? bad.includes(t) : t.status === "passed"));
  const projects = [...new Set(run.tests.map((t) => t.project))].join(" + ");
  return (
    <>
      <div className={`overall ${ok ? "ok" : "bad"}`} role="status" data-testid="automate-overall">
        <span className="ic">{ok ? "✓" : "!"}</span>
        <div className="txt">
          <h2>
            {runLabel(run)}: {run.status === "failed" ? `${plural(run.failed, "test")} failed` : run.status === "empty" ? "no tests ran" : "all tests passed"}
          </h2>
          <span className="muted" style={{ fontSize: 15 }}>
            Started {when(run.startedAt)} · took {dur(run.durationMs)} · LLM provider <b className="mono">{run.llm}</b>
            {projects ? ` · projects ${projects}` : ""}
          </span>
        </div>
        <StatusBadge s={run.status} />
      </div>
      <div className="tiles-4">
        <Total id="automate-passed" label="Passed" n={run.passed} tone="success" />
        <Total id="automate-failed" label="Failed" n={run.failed} tone="danger" bad={run.failed > 0} />
        <Total id="automate-skipped" label="Skipped" n={run.skipped} tone="neutral" />
        <Total id="automate-flaky" label="Flaky" n={run.flaky} tone="warning" />
      </div>
      {run.globalErrors.length > 0 && (
        <div className="banner err" role="alert">
          <div className="txt">
            <h3>Errors outside any test</h3>
            {run.globalErrors.map((e, i) => (
              <pre key={i} className="au-pre">
                {e}
              </pre>
            ))}
          </div>
        </div>
      )}
      <div className="row-gap" style={{ justifyContent: "space-between" }}>
        <div className="tabs" role="group" aria-label="Filter tests">
          {(
            [
              ["all", `All ${run.tests.length}`],
              ["failed", `Failed ${bad.length}`],
              ["passed", `Passed ${run.tests.filter((t) => t.status === "passed").length}`],
            ] as const
          ).map(([v, l]) => (
            <button key={v} type="button" aria-pressed={filter === v} onClick={() => setFilter(v)} data-testid={`automate-filter-${v}`}>
              {l}
            </button>
          ))}
        </div>
        <span className="foot-note">
          {run.id === "latest"
            ? reportOn
              ? "Traces and every attachment: Open HTML report, or pnpm exec playwright show-report."
              : "No HTML report on disk."
            : "The HTML report holds the latest run only; screenshots below are this run's archived copies."}
        </span>
      </div>
      <TestsTable tests={shown} />
    </>
  );
}

function Total({ id, label, n, tone, bad }: { id: string; label: string; n: number; tone: Tone; bad?: boolean }) {
  return (
    <div className={`card tile${bad ? " bad" : ""}`} data-testid={id}>
      <div className="th">
        <span className="caps">{label}</span>
        <Badge word={String(n)} tone={n ? tone : "neutral"} />
      </div>
      <div className="v mono">{n}</div>
    </div>
  );
}

const key = (t: E2ETest) => `${t.project}|${t.file}|${t.line}|${t.title}`;

function TestsTable({ tests }: { tests: E2ETest[] }) {
  // Failures open by default; the operator can fold any row.
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const isOpen = (t: E2ETest) => open[key(t)] ?? (t.status !== "passed" && t.status !== "skipped");
  return (
    <div className="tablewrap">
      <table className="au-table" data-testid="automate-tests">
        <thead>
          <tr>
            <th>Project</th>
            <th>File</th>
            <th>Test</th>
            <th>Status</th>
            <th className="right">Duration</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {tests.length ? (
            tests.map((t) => {
              const k = key(t);
              const o = isOpen(t);
              return (
                <Fragment key={k}>
                  <tr className={t.status === "passed" || t.status === "skipped" ? "" : "au-bad"} data-testid="automate-test-row">
                    <td>
                      <Badge word={t.project} tone={t.project === "ui" ? "operator" : "info"} />
                    </td>
                    <td className="mono au-file">
                      {t.file}:{t.line}
                    </td>
                    <td>
                      <b>{t.title}</b>
                      {t.error && !o ? <div className="au-err1">{t.error.split("\n")[0]}</div> : null}
                    </td>
                    <td>
                      <StatusBadge s={t.status} />
                      {t.results.length > 1 ? <small className="muted"> {plural(t.results.length, "attempt")}</small> : null}
                    </td>
                    <td className="mono right">{dur(t.durationMs)}</td>
                    <td className="right">
                      {t.steps.length || t.error || t.screenshots.length ? (
                        <button className="btn btn-quiet btn-sm" type="button" aria-expanded={o} onClick={() => setOpen((s) => ({ ...s, [k]: !o }))}>
                          {o ? "Hide" : `Steps${t.steps.length ? ` (${t.steps.length})` : ""}`}
                        </button>
                      ) : null}
                    </td>
                  </tr>
                  {o && (
                    <tr className="au-detail">
                      <td colSpan={6}>
                        <Detail t={t} />
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })
          ) : (
            <tr>
              <td colSpan={6}>
                <div className="empty">
                  <b>No tests in this filter</b>
                </div>
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

function Detail({ t }: { t: E2ETest }) {
  return (
    <div className="au-d">
      {t.steps.length > 0 && (
        <ol className="au-steps" aria-label="test.step breakdown">
          {t.steps.map((s, i) => (
            <li key={i} style={{ paddingLeft: s.depth * 20 }} className={s.status === "failed" ? "f" : ""}>
              <span className="au-mark" aria-label={s.status}>
                {s.status === "failed" ? "✕" : "✓"}
              </span>
              <span className="au-st">{s.title}</span>
              <span className="mono muted">{dur(s.durationMs)}</span>
            </li>
          ))}
        </ol>
      )}
      {t.error && <pre className="au-pre" data-testid="automate-test-error">{t.error}</pre>}
      {t.screenshots.length > 0 && (
        <div className="au-shots">
          {t.screenshots.map((s) => (
            <a key={s.url} href={s.url} target="_blank" rel="noreferrer" title={s.name}>
              {/* eslint-disable-next-line @next/next/no-img-element -- served from disk by /api/automate/artifact */}
              <img src={s.url} alt={`Screenshot: ${t.title}`} loading="lazy" />
            </a>
          ))}
        </div>
      )}
    </div>
  );
}

function History({ runs, current, pick }: { runs: E2ERunsResponse; current: string | null; pick: (id: string) => void }) {
  const rows: E2ERunSummary[] = [...(runs.latest ? [runs.latest] : []), ...runs.history];
  return (
    <section className="au-hist" aria-labelledby="au-hist-h">
      <div className="row-gap" style={{ justifyContent: "space-between" }}>
        <h2 id="au-hist-h" className="au-h2">
          Run history
        </h2>
        <span className="foot-note">
          {runs.history.length
            ? `${plural(runs.history.length, "monitor run")} kept in playwright/results/history (last 50)`
            : "No monitor runs yet: make monitor-install schedules one every 60 min."}
        </span>
      </div>
      <div className="tablewrap">
        <table data-testid="automate-history">
          <thead>
            <tr>
              <th>Run</th>
              <th>Started</th>
              <th>Status</th>
              <th className="right">Passed</th>
              <th className="right">Failed</th>
              <th className="right">Skipped</th>
              <th className="right">Flaky</th>
              <th className="right">Duration</th>
              <th>LLM</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id} className={r.id === current ? "hl" : ""} data-testid="automate-history-row">
                <td className="mono">{r.id === "latest" ? "latest.json" : r.id}</td>
                <td>{when(r.startedAt)}</td>
                <td>
                  <StatusBadge s={r.status} />
                </td>
                <td className="mono right">{r.passed}</td>
                <td className="mono right">{r.failed}</td>
                <td className="mono right">{r.skipped}</td>
                <td className="mono right">{r.flaky}</td>
                <td className="mono right">{dur(r.durationMs)}</td>
                <td className="mono">{r.llm}</td>
                <td className="right">
                  {r.id === current ? (
                    <span className="muted" style={{ fontSize: 15 }}>
                      Shown
                    </span>
                  ) : (
                    <button className="btn btn-quiet btn-sm" type="button" onClick={() => pick(r.id)}>
                      View →
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
