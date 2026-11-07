// Email / password fields, kept as in the design but switched off: the password route
// (/api/auth/sign-in) was removed and operators sign in with Google only. To bring password sign-in back,
// restore a server route that checks credentials, then render <PasswordForm enabled next={…} />
// (it then posts to /api/auth/sign-in as a plain HTML form).
export function PasswordForm({ enabled = false, next }: { enabled?: boolean; next?: string }) {
  const off = !enabled;
  return (
    <form
      className="signin-form"
      method="post"
      action={enabled ? "/api/auth/sign-in" : undefined}
      onSubmit={off ? (e) => e.preventDefault() : undefined}
      aria-describedby={off ? "signin-pw-off" : undefined}
    >
      {next ? <input type="hidden" name="next" value={next} /> : null}
      <fieldset disabled={off}>
        <div className="field">
          <label htmlFor="signin-user">Work email / username</label>
          <input className="input" id="signin-user" name="user" autoComplete="username" placeholder="you@sockabus.my" required data-testid="signin-user" />
        </div>
        <div className="field">
          <label htmlFor="signin-password">Password</label>
          <input className="input" id="signin-password" name="password" type="password" autoComplete="current-password" required data-testid="signin-password" />
        </div>
        <button className="btn btn-primary" type="submit" data-testid="signin-submit">
          Sign in →
        </button>
      </fieldset>
      {off ? (
        <p id="signin-pw-off" className="sr-only">
          Password sign-in is turned off. Use Continue with Google.
        </p>
      ) : null}
    </form>
  );
}
