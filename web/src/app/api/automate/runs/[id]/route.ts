// One Playwright run, every test with its steps: id "latest" or a history id (UTC timestamp).
import { connection } from "next/server";
import { getRun, validRunId } from "@/lib/e2e-results";

export async function GET(_req: Request, ctx: RouteContext<"/api/automate/runs/[id]">) {
  await connection();
  const { id } = await ctx.params;
  if (!validRunId(id)) return Response.json({ error: "unknown run id" }, { status: 400 });
  const run = await getRun(id);
  if (!run) return Response.json({ error: `no results for run ${id}` }, { status: 404 });
  return Response.json(run, { headers: { "cache-control": "no-store" } });
}
