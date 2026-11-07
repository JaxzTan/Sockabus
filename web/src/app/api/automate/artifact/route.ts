// A UI test screenshot: ?run=<id>&path=<path under test-results/>. PNG only, never outside its directory.
import { readFile } from "node:fs/promises";
import { connection, type NextRequest } from "next/server";
import { artifactPath, isFile } from "@/lib/e2e-results";

export async function GET(req: NextRequest) {
  await connection();
  const q = req.nextUrl.searchParams;
  const p = artifactPath(q.get("run") ?? "", q.get("path") ?? "");
  if (!p) return Response.json({ error: "bad run or path" }, { status: 400 });
  if (!(await isFile(p))) return Response.json({ error: "screenshot not on disk (overwritten by a later run, or this run was not archived)" }, { status: 404 });
  return new Response(new Uint8Array(await readFile(p)), { headers: { "content-type": "image/png", "cache-control": "private, max-age=60" } });
}
