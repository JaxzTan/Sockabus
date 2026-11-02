// /automate: the latest Playwright run and the monitor's run history (src/lib/e2e-results.ts).
// Behind the sign-in gate like every /api/* route (src/proxy.ts).
import { connection } from "next/server";
import { isFile, latestRun, listRuns, reportDir, resultsDir } from "@/lib/e2e-results";
import type { E2ERunsResponse } from "@/lib/e2e-types";
import path from "node:path";

export async function GET() {
  await connection(); // read the disk on every request, never at build time
  const [latest, history, report] = await Promise.all([latestRun(), listRuns(), isFile(path.join(reportDir(), "index.html"))]);
  const body: E2ERunsResponse = { dir: resultsDir(), latest, history, report };
  return Response.json(body, { headers: { "cache-control": "no-store" } });
}
