"use client";
import Image from "next/image";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Suspense, useEffect, useState, type ReactNode } from "react";
import { DEV_CHAT, useAgo, useSidebar } from "@/lib/hooks";
import { Loading } from "@/components/ui";
import { dismissToast, flushDecisions, liveConvos, queueOf, refresh, resetDemo, startSync, USE_SAMPLE, useStore } from "@/lib/store";

const OPERATE = [
  { href: "/home", label: "Dashboard", id: "overview" },
  { href: "/personas", label: "Personas", id: "personas" },
  { href: "/conversations", label: "Conversations", id: "conversations" },
  { href: "/verify", label: "Verify queue", id: "verify" },
  { href: "/entities", label: "Entities", id: "entities" },
  { href: "/status", label: "Status", id: "status" },
  { href: "/automate", label: "Automate", id: "automate" },
];
const DEMO = [
  { href: "/demo/bank", label: "Bank simulator", id: "bank" },
  { href: "/demo/customer", label: "Customer view", id: "customer" },
  ...(DEV_CHAT ? [{ href: "/demo/chat", label: "Web chat", id: "webchat" }] : []),
];

export function Shell({ children }: { children: ReactNode }) {
  const [open, toggle] = useSidebar();
  return (
    <>
      <div className={`app${open ? "" : " rail-off"}`}>
        <Rail hidden={!open} />
        <div className="work">
          {USE_SAMPLE && (
            <div className="sample-banner" role="note" data-testid="sample-banner">
              <span className="badge t-warning">Sample</span>
              Sample data — not live
            </div>
          )}
          <div className="topbar">
            <button
              type="button"
              className="rail-toggle"
              onClick={toggle}
              aria-expanded={open}
              aria-controls="sidebar"
              data-testid="sidebar-toggle-btn"
            >
              <span aria-hidden="true">{open ? "«" : "☰"}</span>
              {open ? "Hide menu" : "Show menu"}
            </button>
            {!open && (
              <Link href="/home" className="mini-brand" aria-label="Sockabus dashboard">
                <Image src="/logo.png" alt="" width={36} height={24} />
                <b>Sockabus</b>
              </Link>
            )}
          </div>
          <main id="main" tabIndex={-1}>
            <Gate>{children}</Gate>
          </main>
        </div>
      </div>
      <Toasts />
      <Suspense fallback={null}>
        <FlushOnNavigate />
      </Suspense>
    </>
  );
}

// API mode: poll the Go API, and show loading / error until the first answer arrives.
function Gate({ children }: { children: ReactNode }) {
  const { loaded, loadError } = useStore().ui;
  useEffect(startSync, []);
  if (!loaded && !loadError) return <Loading />;
  if (!loaded) {
    const banner = (
      <div className="banner err" role="alert" data-testid="api-error">
        <div className="txt">
          <h3>Can&apos;t load data from Sockabus</h3>
          <p>{loadError}</p>
        </div>
        <div className="acts">
          <button className="btn btn-primary" onClick={() => void refresh()}>
            Try again
          </button>
        </div>
      </div>
    );
    // The Status page is for exactly this moment: show it (red) under the banner instead of hiding it.
    return (
      <Suspense fallback={banner}>
        <UnlessStatus banner={banner}>{children}</UnlessStatus>
      </Suspense>
    );
  }
  return (
    <>
      {loadError && (
        <div className="banner err" role="alert" data-testid="api-error">
          <div className="txt">
            <h3>Connection lost — showing the last data received</h3>
            <p>{loadError} Retrying every 3 s.</p>
          </div>
        </div>
      )}
      {children}
    </>
  );
}

// usePathname is runtime data, so this sits behind Suspense (see NavLinks).
function UnlessStatus({ banner, children }: { banner: ReactNode; children: ReactNode }) {
  return usePathname() === "/status" ? (
    <>
      {banner}
      {children}
    </>
  ) : (
    banner
  );
}

// Leaving a page sends any Verify/Reject still inside its Undo window (spec §4.5).
function FlushOnNavigate() {
  const path = usePathname();
  useEffect(() => flushDecisions(), [path]);
  return null;
}

type NavItem = (typeof OPERATE)[number];

// Every nav item is "here" on its own page and its sub-pages.
const isHere = (path: string | undefined, href: string) => !!path?.startsWith(href);

// usePathname is runtime data, so the active highlight streams in behind Suspense.
function NavLinks({ items, counts }: { items: NavItem[]; counts: Record<string, number> }) {
  return <Links items={items} counts={counts} path={usePathname()} />;
}

function Links({ items, counts, path }: { items: NavItem[]; counts: Record<string, number>; path?: string }) {
  return (
    <nav className="nav">
      {items.map((n) => (
        <Link key={n.id} href={n.href} data-testid={`nav-${n.id}`} aria-current={isHere(path, n.href) ? "page" : undefined}>
          <span>{n.label}</span>
          {counts[n.id] ? <span className="count">{counts[n.id]}</span> : null}
        </Link>
      ))}
    </nav>
  );
}

function Nav({ items, counts }: { items: NavItem[]; counts: Record<string, number> }) {
  return (
    <Suspense fallback={<Links items={items} counts={counts} />}>
      <NavLinks items={items} counts={counts} />
    </Suspense>
  );
}

function Rail({ hidden }: { hidden: boolean }) {
  const { data, ui } = useStore();
  const ago = useAgo();
  const counts: Record<string, number> = { conversations: liveConvos(data).length, verify: queueOf(data).length };
  // Sample mode: the simulated Telegram switch. API mode: GET /readyz and the last poll, never a guess.
  const { health: h, loadError } = ui;
  const ok = USE_SAMPLE ? data.telegram : !!h?.ok && !loadError;
  const title = USE_SAMPLE ? (ok ? "All systems normal" : "Telegram disconnected") : !h ? "Checking backend…" : ok ? "Backend up" : h.status ? "Backend not ready" : "Backend not reachable";
  const sub = USE_SAMPLE
    ? ok
      ? `Telegram connected · last message ${ago}`
      : "Alert sent to team · use Web chat"
    : ok
      ? `Data updated ${ago}`
      : "Open Status for details";
  return (
    <aside className="rail" id="sidebar" aria-label="Sockabus navigation" hidden={hidden}>
      <Link href="/home" className="brand" aria-label="Sockabus dashboard">
        <Image src="/logo.png" alt="Sockabus mascot" width={52} height={34} priority />
        <div>
          <b>Sockabus</b>
          <i />
        </div>
      </Link>
      <div className="group caps">Operate</div>
      <Nav items={OPERATE} counts={counts} />
      <div className="group caps">Demo</div>
      <Nav items={DEMO} counts={counts} />
      <div className="spacer" />
      <div className="sys" data-testid="sidebar-status">
        <div className="row">
          <span className="dot" style={{ color: ok ? "var(--green)" : !USE_SAMPLE && !h ? "var(--faint)" : "var(--red)" }} />
          {title}
        </div>
        <small>{sub}</small>
      </div>
      <SignedIn />
      {DEV_CHAT && (
        <div>
          <button className="reset" type="button" onClick={resetDemo} data-testid="reset-demo">
            Reset demo data
          </button>
        </div>
      )}
    </aside>
  );
}

// Who is signed in (GET /api/auth/me reads the httpOnly cookie on the server) and Sign out.
function SignedIn() {
  const [me, setMe] = useState<{ user: string | null; gate: boolean } | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let live = true;
    fetch("/api/auth/me", { cache: "no-store" })
      .then((r) => (r.ok ? r.json() : null))
      .then((j) => live && j && setMe(j))
      .catch(() => {});
    return () => {
      live = false;
    };
  }, []);
  const signOut = async () => {
    setBusy(true);
    flushDecisions(true); // a decision still in its Undo window is sent, never lost
    await fetch("/api/auth/sign-out", { method: "POST" }).catch(() => {});
    // Full page load on purpose: drops all in-memory data and lets the proxy check the cookie again.
    // eslint-disable-next-line @next/next/no-location-assign-relative-destination
    location.assign("/sign-in");
  };
  if (!me) return <div className="who">&nbsp;</div>;
  if (!me.gate) return <div className="who">Sign-in is off (development)</div>;
  return (
    <>
      <div className="who" data-testid="signed-in-user">
        Signed in as <b>{me.user}</b>
      </div>
      <div>
        <button className="reset" type="button" onClick={signOut} disabled={busy} data-testid="signout-btn">
          Sign out
        </button>
      </div>
    </>
  );
}

function Toasts() {
  const { toasts } = useStore().ui;
  return (
    <div className="toasts" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} className="toast" data-testid="verify-toast" role={t.error ? "alert" : undefined}>
          {t.error ? <span className="err">!</span> : <span className="ok">✓</span>}
          <span>{t.msg}</span>
          {t.href ? (
            <Link href={t.href} onClick={() => dismissToast(t.id)} style={{ color: "var(--mustard)", fontWeight: 700, textDecoration: "none" }}>
              {t.label}
            </Link>
          ) : t.run ? (
            <button
              type="button"
              data-testid={t.testId}
              onClick={() => {
                t.run?.();
                dismissToast(t.id);
              }}
            >
              {t.label}
            </button>
          ) : null}
        </div>
      ))}
    </div>
  );
}
