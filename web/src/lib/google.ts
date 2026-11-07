// Google sign-in (OpenID Connect, authorization code + PKCE S256), server only. No auth library: the flow
// is small enough to read in one sitting, and node:crypto + fetch cover it.
//
// Trust chain: the browser authenticates to Google; Google hands our callback a one-time code; this server
// swaps the code (plus the PKCE verifier and client secret) for an ID token directly at Google's token
// endpoint over TLS. Because the token comes straight from Google on that authenticated back channel, the ID
// token's JWS signature is not re-verified against Google's JWKS (allowed by OIDC Core §3.1.3.7 step 6);
// its claims (iss, aud, exp, nonce, email_verified) are still checked below.
import { randomBytes, createHash } from "node:crypto";
import { publicOrigin, same, sign } from "@/lib/auth";

export const FLOW_COOKIE = "sockabus_oauth";
export const FLOW_PATH = "/api/auth/google"; // the cookie is only sent to /api/auth/google and its callback
export const FLOW_MAX_AGE = 10 * 60; // seconds to finish signing in at Google

const AUTHORIZE = "https://accounts.google.com/o/oauth2/v2/auth";
const TOKEN = "https://oauth2.googleapis.com/token";
const ISSUERS = new Set(["https://accounts.google.com", "accounts.google.com"]);
const SKEW_MS = 60_000;

const clientId = () => process.env.GOOGLE_CLIENT_ID ?? "";
const clientSecret = () => process.env.GOOGLE_CLIENT_SECRET ?? "";
export const googleConfigured = () => clientId() !== "" && clientSecret() !== "";

// GOOGLE_REDIRECT_URI wins (it must be registered in Google Cloud Console exactly as written); otherwise the
// origin the browser used + /api/auth/google/callback.
export function redirectUri(req: Request): string | null {
  const fixed = process.env.GOOGLE_REDIRECT_URI?.trim();
  if (fixed) {
    try {
      const u = new URL(fixed);
      return u.protocol === "https:" || u.protocol === "http:" ? u.toString() : null;
    } catch {
      return null;
    }
  }
  const origin = publicOrigin(req);
  return origin ? `${origin}${FLOW_PATH}/callback` : null;
}

// Per-attempt secrets, kept in a short-lived httpOnly cookie signed with the session secret.
export type Flow = { state: string; nonce: string; verifier: string; next: string; redirectUri: string; exp: number };

const rand = () => randomBytes(32).toString("base64url");

export function startFlow(next: string, redirect: string): { flow: Flow; cookie: string; url: string } {
  const flow: Flow = { state: rand(), nonce: rand(), verifier: rand(), next, redirectUri: redirect, exp: Date.now() + FLOW_MAX_AGE * 1000 };
  const body = Buffer.from(JSON.stringify(flow)).toString("base64url");
  const cookie = `${body}.${sign("oauth-flow", body)}`;
  const url = new URL(AUTHORIZE);
  url.search = new URLSearchParams({
    response_type: "code",
    client_id: clientId(),
    redirect_uri: redirect,
    scope: "openid email",
    state: flow.state,
    nonce: flow.nonce,
    code_challenge: createHash("sha256").update(flow.verifier).digest("base64url"),
    code_challenge_method: "S256",
    prompt: "select_account",
  }).toString();
  return { flow, cookie, url: url.toString() };
}

export function readFlow(cookie: string | undefined): Flow | null {
  if (!cookie) return null;
  const [body, mac, extra] = cookie.split(".");
  if (!body || !mac || extra !== undefined || !same(mac, sign("oauth-flow", body))) return null;
  try {
    const f = JSON.parse(Buffer.from(body, "base64url").toString()) as Flow;
    return typeof f.state === "string" && f.exp > Date.now() ? f : null;
  } catch {
    return null;
  }
}

export class SignInError extends Error {
  constructor(
    readonly code: "exchange" | "invalid" | "unverified",
    readonly reason: string,
  ) {
    super(reason);
  }
}

// Code -> ID token at Google's token endpoint, then the claim checks. Returns the verified email.
export async function exchange(code: string, flow: Flow): Promise<string> {
  const res = await fetch(TOKEN, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
    body: new URLSearchParams({
      grant_type: "authorization_code",
      code,
      code_verifier: flow.verifier,
      redirect_uri: flow.redirectUri,
      client_id: clientId(),
      client_secret: clientSecret(),
    }),
    cache: "no-store",
    signal: AbortSignal.timeout(10_000),
  }).catch(() => null);
  if (!res) throw new SignInError("exchange", "token_endpoint_unreachable");
  const json = (await res.json().catch(() => null)) as { id_token?: unknown; error?: unknown } | null;
  if (!res.ok || typeof json?.id_token !== "string") throw new SignInError("exchange", `token_endpoint_${res.status}_${String(json?.error ?? "no_id_token")}`);

  const parts = json.id_token.split(".");
  if (parts.length !== 3) throw new SignInError("invalid", "id_token_malformed");
  let claims: Record<string, unknown>;
  try {
    claims = JSON.parse(Buffer.from(parts[1], "base64url").toString());
  } catch {
    throw new SignInError("invalid", "id_token_malformed");
  }

  const now = Date.now();
  const aud = claims.aud;
  if (!ISSUERS.has(String(claims.iss))) throw new SignInError("invalid", "iss");
  if (!(aud === clientId() || (Array.isArray(aud) && aud.includes(clientId())))) throw new SignInError("invalid", "aud");
  if (Array.isArray(aud) && aud.length > 1 && claims.azp !== clientId()) throw new SignInError("invalid", "azp");
  if (!(typeof claims.exp === "number" && claims.exp * 1000 + SKEW_MS > now)) throw new SignInError("invalid", "exp");
  if (typeof claims.iat === "number" && claims.iat * 1000 - SKEW_MS > now) throw new SignInError("invalid", "iat");
  if (typeof claims.nonce !== "string" || !same(claims.nonce, flow.nonce)) throw new SignInError("invalid", "nonce");
  if (typeof claims.email !== "string" || claims.email === "") throw new SignInError("invalid", "no_email");
  if (claims.email_verified !== true && claims.email_verified !== "true") throw new SignInError("unverified", "email_not_verified");
  return claims.email.toLowerCase();
}
