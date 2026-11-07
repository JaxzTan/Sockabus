// Console sign-in (server only: imported by src/proxy.ts and the /api/auth routes, never by the browser).
// Operators sign in with Google (OIDC authorization code + PKCE, src/app/api/auth/google/*). Only emails on
// the allowlist get in (SOCKABUS_CONSOLE_GOOGLE_ALLOWED_EMAILS and/or SOCKABUS_CONSOLE_GOOGLE_ALLOWED_DOMAIN);
// with neither set nobody can sign in. The session cookie is "<b64url email>.<expiry ms>.<HMAC>", httpOnly,
// signed with SOCKABUS_SESSION_SECRET: rotating the secret signs everyone out, and removing an email from the
// allowlist ends that operator's session on their next request. SOCKABUS_CONSOLE_AUTH=off turns the gate off
// (local development only).
import { createHash, createHmac, timingSafeEqual } from "node:crypto";
import { NextResponse } from "next/server";

export const SESSION_COOKIE = "sockabus_session";
export const SESSION_MAX_AGE = 12 * 60 * 60; // seconds
export const HOME = "/home";

const MIN_SECRET = 32;
const secret = () => process.env.SOCKABUS_SESSION_SECRET ?? "";

export const authOff = () => ["off", "false", "0"].includes((process.env.SOCKABUS_CONSOLE_AUTH ?? "").toLowerCase());
// A short secret is treated as no secret: sessions would be guessable.
export const authConfigured = () => secret().length >= MIN_SECRET;

const sha = (s: string) => createHash("sha256").update(s).digest();
// Compare hashes so the time taken doesn't depend on where the strings differ.
export const same = (a: string, b: string) => timingSafeEqual(sha(a), sha(b));
// Domain-separated HMAC keys from the one secret: a session MAC can never pass as an OAuth-flow MAC.
export const sign = (purpose: string, payload: string) =>
  createHmac("sha256", sha(`sockabus-console:${purpose}:` + secret())).update(payload).digest("base64url");

const list = (v: string | undefined) =>
  (v ?? "")
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);

// Deny by default: an email gets in only when it is listed, or its domain is.
export function emailAllowed(email: string): boolean {
  const e = email.trim().toLowerCase();
  const at = e.lastIndexOf("@");
  if (at < 1 || at === e.length - 1) return false;
  const emails = list(process.env.SOCKABUS_CONSOLE_GOOGLE_ALLOWED_EMAILS);
  const domains = list(process.env.SOCKABUS_CONSOLE_GOOGLE_ALLOWED_DOMAIN).map((d) => d.replace(/^@/, ""));
  return emails.includes(e) || domains.includes(e.slice(at + 1));
}
export const allowlistConfigured = () =>
  list(process.env.SOCKABUS_CONSOLE_GOOGLE_ALLOWED_EMAILS).length > 0 || list(process.env.SOCKABUS_CONSOLE_GOOGLE_ALLOWED_DOMAIN).length > 0;

export function makeSession(email: string): string {
  const payload = `${Buffer.from(email.trim().toLowerCase()).toString("base64url")}.${Date.now() + SESSION_MAX_AGE * 1000}`;
  return `${payload}.${sign("session", payload)}`;
}

// The signed-in email, or null when the cookie is missing, forged, expired or no longer on the allowlist.
export function readSession(cookie: string | undefined): string | null {
  if (!cookie || !authConfigured()) return null;
  const parts = cookie.split(".");
  if (parts.length !== 3) return null;
  const [e64, exp, mac] = parts;
  if (!same(mac, sign("session", `${e64}.${exp}`))) return null;
  if (!(Number(exp) > Date.now())) return null;
  const email = Buffer.from(e64, "base64url").toString();
  return emailAllowed(email) ? email : null;
}

// Only a same-site path after sign-in, never "//evil.example", "/\\evil.example" or a full URL. Browsers drop
// tabs and newlines inside URLs ("/\t/evil" becomes "//evil"), so any control character, space or
// backslash is refused outright. Anything else (and "/" itself) falls back to the dashboard.
export function safeNext(n: string | null | undefined): string {
  if (!n || n === "/" || !n.startsWith("/") || n.startsWith("//") || /[\u0000- \u007f\\]/.test(n)) return HOME;
  try {
    const u = new URL(n, "http://sockabus.invalid");
    if (u.origin !== "http://sockabus.invalid") return HOME;
    return u.pathname + u.search + u.hash;
  } catch {
    return HOME;
  }
}

// Redirect with a relative Location ("/verify", "/sign-in?next=…"). Never built from request.url: in the
// standalone server that is the bind address (http://0.0.0.0:3000), which the browser can't open.
export function redirectTo(path: string, status: 303 | 307 = 307): NextResponse {
  return new NextResponse(null, { status, headers: { Location: path, "Cache-Control": "no-store" } });
}

// The origin the browser used, from X-Forwarded-Host (Caddy) or Host, never request.url's host (the bind
// address). Needed where Next requires an absolute redirect: Location headers returned from proxy.ts
// are parsed with new URL(), so a relative path throws there. Odd-looking hosts are refused.
const HOST = /^[a-z0-9.-]+(:\d{1,5})?$|^\[[0-9a-f:.]+\](:\d{1,5})?$/i;
export function publicOrigin(req: Request): string | null {
  const first = (h: string | null) => h?.split(",")[0]?.trim() ?? "";
  const host = first(req.headers.get("x-forwarded-host")) || first(req.headers.get("host"));
  if (!host || !HOST.test(host)) return null;
  return `${isHttps(req) ? "https" : "http"}://${host}`;
}

// HTTPS as the browser sees it: behind Caddy the app itself only gets plain HTTP.
export function isHttps(req: Request): boolean {
  const fwd = req.headers.get("x-forwarded-proto")?.split(",")[0]?.trim().toLowerCase();
  return fwd ? fwd === "https" : new URL(req.url).protocol === "https:";
}

export const cookieOptions = (secure: boolean) => ({ httpOnly: true, sameSite: "lax" as const, secure, path: "/", maxAge: SESSION_MAX_AGE });

// One structured line per auth event, for the audit trail. Never pass tokens, codes or secrets here.
export function authEvent(event: string, detail: Record<string, string | undefined> = {}) {
  console.info(JSON.stringify({ at: new Date().toISOString(), scope: "console-auth", event, ...detail }));
}
