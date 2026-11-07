// GET /api/auth/google?next=/verify: start Google sign-in. Fresh state, nonce and PKCE verifier go into a
// short-lived, signed, httpOnly cookie scoped to /api/auth/google; the browser is sent to Google.
import { NextResponse } from "next/server";
import { allowlistConfigured, authConfigured, authEvent, authOff, isHttps, redirectTo, safeNext } from "@/lib/auth";
import { FLOW_COOKIE, FLOW_MAX_AGE, FLOW_PATH, googleConfigured, redirectUri, startFlow } from "@/lib/google";

export async function GET(req: Request) {
  const next = safeNext(new URL(req.url).searchParams.get("next"));
  if (authOff()) return redirectTo(next);
  const redirect = redirectUri(req);
  if (!authConfigured() || !googleConfigured() || !allowlistConfigured() || !redirect) {
    authEvent("google_start_misconfigured", {
      session_secret: authConfigured() ? "ok" : "missing_or_short",
      google_client: googleConfigured() ? "ok" : "missing",
      allowlist: allowlistConfigured() ? "ok" : "missing",
      redirect_uri: redirect ? "ok" : "invalid",
    });
    return redirectTo("/sign-in?error=setup");
  }
  const { cookie, url } = startFlow(next, redirect);
  const res = NextResponse.redirect(url, 307);
  res.headers.set("Cache-Control", "no-store");
  res.cookies.set(FLOW_COOKIE, cookie, { httpOnly: true, sameSite: "lax", secure: isHttps(req), path: FLOW_PATH, maxAge: FLOW_MAX_AGE });
  return res;
}
