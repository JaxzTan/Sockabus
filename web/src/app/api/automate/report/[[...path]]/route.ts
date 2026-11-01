// The Playwright HTML report of the last run (playwright-report/), served read-only behind the sign-in gate.
// /api/automate/report → index.html; its relative data/ and trace/ URLs resolve under the same prefix.
import { readFile } from "node:fs/promises";
import path from "node:path";
import { connection } from "next/server";
import { inside, isFile, reportDir } from "@/lib/e2e-results";

const TYPES: Record<string, string> = {
  ".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8", ".json": "application/json", ".md": "text/markdown; charset=utf-8", ".txt": "text/plain; charset=utf-8",
  ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".svg": "image/svg+xml", ".webm": "video/webm",
  ".zip": "application/zip", ".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf", ".ico": "image/x-icon",
};

export async function GET(_req: Request, ctx: RouteContext<"/api/automate/report/[[...path]]">) {
  await connection();
  const parts = (await ctx.params).path ?? [];
  // Without a trailing file name the report's relative URLs would resolve one level up.
  // Relative Location: req.url carries the bind address, not the host the browser used (see src/proxy.ts).
  if (parts.length === 0) return new Response(null, { status: 307, headers: { location: "/api/automate/report/index.html" } });
  const p = inside(reportDir(), parts.join("/"));
  if (!p) return Response.json({ error: "bad path" }, { status: 400 });
  if (!(await isFile(p))) return Response.json({ error: "not in the HTML report (run the suite first)" }, { status: 404 });
  return new Response(new Uint8Array(await readFile(p)), {
    headers: { "content-type": TYPES[path.extname(p).toLowerCase()] ?? "application/octet-stream", "cache-control": "no-store" },
  });
}
