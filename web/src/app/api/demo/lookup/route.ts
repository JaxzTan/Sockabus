// Bank simulator lookup (S5). The browser sends bank + account here; this route calls
// POST /v1/lookup with the lookup token, so the token never reaches the browser.
import { clientIP, goAPI, lookupToken } from "@/lib/server-api";

export async function POST(req: Request) {
  let bank = "";
  let account = "";
  try {
    const b = (await req.json()) as { bank?: unknown; account?: unknown };
    bank = typeof b.bank === "string" ? b.bank : "";
    account = typeof b.account === "string" ? b.account.replace(/\D/g, "") : "";
  } catch {
    // fall through to the validation error below
  }
  if (!account) return Response.json({ error: "account is required" }, { status: 400 });

  const t0 = performance.now();
  const res = await goAPI("/v1/lookup", {
    method: "POST",
    contentType: "application/json",
    token: lookupToken(),
    body: JSON.stringify({ type: "bank_account", value: account, bank }),
    clientIP: clientIP(req),
  });
  const ms = Math.round(performance.now() - t0);
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) return Response.json(json ?? { error: `lookup failed (${res.status})` }, { status: res.status });
  return Response.json({ res: json, ms });
}
