"use client";
import Image from "next/image";
import { useSearchParams } from "next/navigation";
import { PasswordForm } from "./password-form";

// ?error= codes from /api/auth/google and its callback. The exact reason is only in the server log.
const ERRORS: Record<string, string> = {
  denied: "Google sign-in was cancelled. Try again when you're ready.",
  state: "That sign-in link expired or was already used. Start again.",
  not_allowed: "That Google account isn't on the operator list. Ask an admin to add it.",
  unverified: "Google hasn't verified that email address yet.",
  invalid: "Google's answer didn't check out. Try again.",
  exchange: "Couldn't reach Google to finish signing in. Try again.",
  setup: "Google sign-in isn't set up on this server yet. Ask an admin.",
};

export function SignInView() {
  const sp = useSearchParams();
  const error = ERRORS[sp.get("error") ?? ""];
  const next = sp.get("next") ?? "";
  // Plain link, not fetch: the browser has to follow the redirects to Google and back itself.
  const google = next ? `/api/auth/google?next=${encodeURIComponent(next)}` : "/api/auth/google";
  return (
    <div className="signin">
      <aside className="signin-side">
        <div className="brand">
          <Image src="/logo.png" alt="Sockabus mascot" width={52} height={34} priority />
          <div>
            <b>Sockabus</b>
            <i />
          </div>
        </div>
        <div className="signin-pitch">
          <h1>The operator console for a scam honeypot.</h1>
          <ul>
            <li>AI personas chat with scammers and capture the accounts they share.</li>
            <li>A human verifies every account before any bank sees it.</li>
            <li>Banks look it up before a transfer leaves.</li>
          </ul>
        </div>
        <small>Sockabus Console · demo build</small>
      </aside>
      <main className="signin-main">
        <section className="card signin-card" aria-labelledby="signin-title">
          <h2 id="signin-title">Sign in</h2>
          <p className="muted">Operators only. Pick one way to sign in.</p>
          {error && (
            <div className="banner err" role="alert" data-testid="signin-error">
              <div className="txt">
                <p>{error}</p>
              </div>
            </div>
          )}
          <PasswordForm />
          <div className="signin-or" aria-hidden="true">
            <span>or</span>
          </div>
          <a className="btn btn-google" href={google} data-testid="signin-google">
            <GoogleG />
            Continue with Google
          </a>
          <p className="muted signin-help">Use your operator Google account. Forgot your password? Ask an admin.</p>
        </section>
      </main>
    </div>
  );
}

// Google's four-colour "G" (brand guidelines: keep the colours, don't recolour it).
function GoogleG() {
  return (
    <svg width="20" height="20" viewBox="0 0 48 48" aria-hidden="true" focusable="false">
      <path fill="#FFC107" d="M43.6 20.5H42V20H24v8h11.3C33.7 32.7 29.2 36 24 36c-6.6 0-12-5.4-12-12s5.4-12 12-12c3.1 0 5.8 1.2 7.9 3.1l5.7-5.7C34 6.1 29.3 4 24 4 12.9 4 4 12.9 4 24s8.9 20 20 20 20-8.9 20-20c0-1.3-.1-2.4-.4-3.5z" />
      <path fill="#FF3D00" d="M6.3 14.7l6.6 4.8C14.7 15.1 19 12 24 12c3.1 0 5.8 1.2 7.9 3.1l5.7-5.7C34 6.1 29.3 4 24 4 16.3 4 9.7 8.3 6.3 14.7z" />
      <path fill="#4CAF50" d="M24 44c5.2 0 9.9-2 13.4-5.2l-6.2-5.2C29.2 35.1 26.7 36 24 36c-5.2 0-9.6-3.3-11.3-8l-6.5 5C9.5 39.6 16.2 44 24 44z" />
      <path fill="#1976D2" d="M43.6 20.5H42V20H24v8h11.3c-.8 2.2-2.2 4.2-4.1 5.6l6.2 5.2C37 39.2 44 34 44 24c0-1.3-.1-2.4-.4-3.5z" />
    </svg>
  );
}
