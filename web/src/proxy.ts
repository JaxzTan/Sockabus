// Sign-in gate (Next 16 renamed middleware.ts to proxy.ts). Runs before every page and /api/* route.
// "/" goes to /home when signed in, else /sign-in. Pages without a valid session go to /sign-in; /api/*
// answers 401 JSON (the browser code then opens /sign-in). A signed-in visit to /sign-in goes on to /home.
import { NextResponse, type NextRequest } from "next/server";
import { authOff, HOME, publicOrigin, readSession, safeNext, SESSION_COOKIE } from "@/lib/auth";
import { allow } from "@/lib/ratelimit";

const PUBLIC = new Set(["/sign-in", "/api/auth/google", "/api/auth/google/callback"]);

export function proxy(req: NextRequest) {
  const { pathname, search } = req.nextUrl;
  // Sign-in routes: 20 per minute per client IP (Caddy's X-Forwarded-For), then 429.
  if (pathname.startsWith("/api/auth/")) {
    const ip = (req.headers.get("x-forwarded-for") ?? "").split(",").at(-1)?.trim() || "unknown";
    if (!allow(`auth|${ip}`, 20, 60_000)) {
      return NextResponse.json({ error: "Too many sign-in attempts. Try again in a minute." }, { status: 429, headers: { "retry-after": "60" } });
    }
  }
  const off = authOff();
  const signedIn = off || !!readSession(req.cookies.get(SESSION_COOKIE)?.value);

  // Next parses a proxy redirect's Location, so it must be absolute: build it from the host the browser
  // used (X-Forwarded-Host / Host), never from req.url, which is the bind address (http://0.0.0.0:3000).
  const go = (path: string) => {
    const origin = publicOrigin(req);
    if (!origin) return new NextResponse("Bad Host header", { status: 400 });
    return NextResponse.redirect(origin + path, 307);
  };

  if (pathname === "/") return go(signedIn ? HOME : "/sign-in");
  if (pathname === "/sign-in" && signedIn) return go(safeNext(req.nextUrl.searchParams.get("next")));
  if (off || PUBLIC.has(pathname) || signedIn) return NextResponse.next();
  if (pathname.startsWith("/api/")) return NextResponse.json({ error: "Sign in to use the console API.", sign_in: "/sign-in" }, { status: 401 });
  return go(`/sign-in?next=${encodeURIComponent(pathname + search)}`);
}

// Everything except build assets and the public images the sign-in page itself needs.
export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico|icon.png|logo.png|.*\\.woff2$).*)"],
};
