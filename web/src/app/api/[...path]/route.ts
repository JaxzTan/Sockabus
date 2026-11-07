// Server-side proxy: browser → /api/* → Go API. The operator token is added here, so it
// never reaches the browser. Replaces the next.config rewrite, which can't add headers.
import type { NextRequest } from "next/server";
import { clientIP, goAPI } from "@/lib/server-api";

async function proxy(req: NextRequest, ctx: RouteContext<"/api/[...path]">) {
  const { path } = await ctx.params;
  // The customer lookup goes only through /api/demo/lookup (spec S5): no open /v1/* relay.
  if (path[0] === "v1") return Response.json({ error: "not found" }, { status: 404 });
  const body = req.method === "GET" || req.method === "HEAD" ? undefined : await req.arrayBuffer();
  const res = await goAPI(`/${path.map(encodeURIComponent).join("/")}${req.nextUrl.search}`, {
    method: req.method,
    body,
    contentType: req.headers.get("content-type"),
    signal: req.signal,
    clientIP: clientIP(req),
  });
  const headers = new Headers({ "content-type": res.headers.get("content-type") ?? "application/json" });
  const retry = res.headers.get("retry-after"); // 429 from the Go rate limiter
  if (retry) headers.set("retry-after", retry);
  return new Response(res.body, { status: res.status, headers });
}

export { proxy as DELETE, proxy as GET, proxy as PATCH, proxy as POST, proxy as PUT };
