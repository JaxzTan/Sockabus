// GET /api/auth/google/callback?code=…&state=…: Google sends the browser back here. Every check happens
// before a session exists: flow cookie (signed, unexpired) -> state -> code exchange with the PKCE verifier
// -> ID token claims (lib/google.ts) -> env allowlist -> Go backend allowlist. The flow cookie is cleared on every outcome, so a callback
// can only be used once. Failures go to /sign-in?error=<code>; the reason is only in the server log.
import { cookies } from "next/headers";
import { authConfigured, authEvent, cookieOptions, emailAllowed, HOME, isHttps, makeSession, redirectTo, same, SESSION_COOKIE } from "@/lib/auth";
import { exchange, FLOW_COOKIE, FLOW_PATH, googleConfigured, readFlow, SignInError } from "@/lib/google";
import { clientIP, goAPI } from "@/lib/server-api";

// The Go backend holds the authoritative allowlist (internal/server/consoleauth.go). Fail closed: only a
// 200 {"allowed":true} lets the sign-in through; a 403, a 401 (token mismatch), an unreachable API or a
// malformed answer all refuse it.
async function backendAllows(email: string, ip: string | null): Promise<boolean> {
  const res = await goAPI("/internal/auth/google/check", {
    method: "POST",
    body: JSON.stringify({ email }),
    contentType: "application/json",
    clientIP: ip,
  });
  if (res.status !== 200) return false;
  const body: unknown = await res.json().catch(() => null);
  return typeof body === "object" && body !== null && (body as { allowed?: unknown }).allowed === true;
}

export async function GET(req: Request) {
  const q = new URL(req.url).searchParams;
  const flow = readFlow((await cookies()).get(FLOW_COOKIE)?.value);
  const secure = isHttps(req);

  const fail = (error: string, reason: string, email?: string) => {
    authEvent("google_sign_in_failed", { reason, email });
    const p = new URLSearchParams({ error });
    if (flow && flow.next !== HOME) p.set("next", flow.next);
    const res = redirectTo(`/sign-in?${p}`, 303);
    res.cookies.set(FLOW_COOKIE, "", { httpOnly: true, sameSite: "lax", secure, path: FLOW_PATH, maxAge: 0 });
    return res;
  };

  if (!authConfigured() || !googleConfigured()) return fail("setup", "not_configured");
  if (!flow) return fail("state", "flow_cookie_missing_or_expired");
  const state = q.get("state") ?? "";
  if (!state || !same(state, flow.state)) return fail("state", "state_mismatch");
  // The user cancelled at Google, or Google refused (only trusted once state matched this browser's flow).
  if (q.get("error")) return fail("denied", `google_error_${q.get("error")!.slice(0, 64)}`);
  const code = q.get("code");
  if (!code) return fail("state", "no_code");

  let email: string;
  try {
    email = await exchange(code, flow);
  } catch (e) {
    if (e instanceof SignInError) return fail(e.code, e.reason);
    return fail("exchange", "unexpected");
  }
  if (!emailAllowed(email)) return fail("not_allowed", "not_on_allowlist", email);
  if (!(await backendAllows(email, clientIP(req)).catch(() => false))) return fail("not_allowed", "backend_refused_or_unreachable", email);

  authEvent("google_sign_in", { email });
  const res = redirectTo(flow.next, 303);
  res.cookies.set(FLOW_COOKIE, "", { httpOnly: true, sameSite: "lax", secure, path: FLOW_PATH, maxAge: 0 });
  // A fresh session value on every sign-in (new expiry + MAC), so a pre-set cookie can't be fixated.
  res.cookies.set(SESSION_COOKIE, makeSession(email), cookieOptions(secure));
  return res;
}
