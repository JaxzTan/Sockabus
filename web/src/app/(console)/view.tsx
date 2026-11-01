"use client";
// Dashboard (/), laid out like the design (docs/front-end/sockabus-console, gallery/dashboard.png).
// Built from what store.ts already polls; every number is counted from that data. The "Summary ·
// today" card adds the AI overview from GET /internal/summary (written by Claude on the Go side) above
// the counted lines.
import Link from "next/link";
import type { ReactNode } from "react";
import { Avatar, Badge, Chip, Head, Spin, type Tone } from "@/components/ui";
import { JustVerified } from "@/components/verified";
import { isSimAccount } from "@/lib/api";
import { BANKS, plural, showVal, STAGES, typeLabel } from "@/lib/format";
import { DEV_CHAT, useAgo } from "@/lib/hooks";
import { activePersonas, decide, liveConvos, queueOf, USE_SAMPLE, useStore, type Health, type State } from "@/lib/store";
import { summaryTime, useAISummary } from "@/lib/summary";
import type { Conversation, Entity, EntityType, Persona } from "@/lib/types";

const STUCK_MS = 2 * 60 * 60 * 1000; // no message for 2 h on an open chat
const CHECK_NAMES: Record<string, string> = { postgres: "Database", redis: "Redis" };
const TYPE_WORDS: Record<EntityType, [one: string, many: string]> = {
  bank_account: ["bank account", "bank accounts"],
  wallet: ["wallet", "wallets"],
  phone: ["phone number", "phone numbers"],
  url: ["link", "links"],
  handle: ["handle", "handles"],
};

const count = (n: number, [one, many]: [string, string]) => `${n} ${n === 1 ? one : many}`;
const sameDay = (ms: number, ref: number) => new Date(ms).toDateString() === new Date(ref).toDateString();
const clockOf = (ms: number) => new Date(ms).toTimeString().slice(0, 5);

function StatTile({ id, href, label, value, sub, word, tone }: { id: string; href: string; label: string; value: string; sub: string; word: string; tone: Tone }) {
  return (
    <Link href={href} className="card tile" data-testid={id}>
      <div className="th">
        <span className="caps">{label}</span>
        <Badge word={word} tone={tone} />
      </div>
      <div className="v">{value}</div>
      <small>{sub}</small>
    </Link>
  );
}

export function OverviewView() {
  const st = useStore();
  const { data, ui } = st;
  const queue = queueOf(data);
  const live = liveConvos(data);
  const now = ui.lastSync; // the time this data is from (render stays pure)
  const timed = data.convos.some((c) => c.lastAt !== undefined);
  const stuck = new Set(live.filter((c) => c.lastAt !== undefined && now - c.lastAt > STUCK_MS).map((c) => c.id));
  const onOperator = live.filter((c) => c.status === "operator").length;
  const verified = data.entities.filter((e) => e.status === "verified").length;
  const active = activePersonas(data);

  // "2 bank accounts, 3 handles in the Verify queue"
  const byType = new Map<EntityType, number>();
  queue.forEach((e) => byType.set(e.type, (byType.get(e.type) ?? 0) + 1));
  const queueMix = [...byType.entries()].sort((a, b) => b[1] - a[1]).map(([t, n]) => count(n, TYPE_WORDS[t]));

  // "2 on web chat · 2 not bound to Telegram"
  const onWeb = active.filter((p) => p.handle === "web chat only").length;
  const unbound = active.filter((p) => p.handle === "not bound").length;
  const onTelegram = active.length - onWeb - unbound;
  const personaMix = [onTelegram && `${onTelegram} on Telegram`, onWeb && `${onWeb} on web chat`, unbound && `${unbound} not bound to Telegram`].filter(Boolean).join(" · ");

  return (
    <>
      <Head
        title="Dashboard"
        sub="What the honeypot did today, and what needs a human next."
        right={
          queue.length ? (
            <Link className="btn btn-primary" href="/verify" data-testid="overview-review-btn">
              Review {queue.length} in queue →
            </Link>
          ) : (
            <Badge word="queue clear" tone="success" />
          )
        }
      />

      <JustVerified />

      <div className="ov-grid">
        <div className="ov-left">
          <div className="tiles-4 ov-tiles">
            <StatTile
              id="overview-tile-convos"
              href="/conversations"
              label="Live conversations"
              value={String(live.length)}
              sub={[
                live.length ? (onOperator ? `${onOperator} with an operator` : "All bot-run") : "No open chats",
                timed && live.length ? (stuck.size ? `${stuck.size} stuck over 2 h` : "none stuck over 2 h") : "",
              ]
                .filter(Boolean)
                .join(" · ")}
              word="running"
              tone="info"
            />
            <StatTile
              id="overview-tile-queue"
              href="/verify"
              label="Waiting for a human"
              value={String(queue.length)}
              sub={queue.length ? `${queueMix.join(", ")} in the Verify queue` : "Queue clear"}
              word={queue.length ? "unverified" : "clear"}
              tone={queue.length ? "warning" : "success"}
            />
            <StatTile
              id="overview-tile-accounts"
              href="/entities"
              label="Entities captured"
              value={String(data.entities.length)}
              sub={verified ? `${verified} verified · banks get a match for ${verified === 1 ? "it" : "them"}` : "0 verified yet · banks see none of them"}
              word={USE_SAMPLE ? "sample" : "live"}
              tone={USE_SAMPLE ? "neutral" : "info"}
            />
            <StatTile
              id="overview-tile-personas"
              href="/personas?status=active"
              label="Active personas"
              value={`${active.length} of ${data.personas.length}`}
              sub={personaMix || "None active yet"}
              word="active"
              tone="success"
            />
          </div>
          <ActiveChats st={st} live={live} stuck={stuck} />
          <PersonaCards personas={data.personas} convos={data.convos} />
        </div>
        <div className="ov-right">
          <Today st={st} />
          <NextInQueue queue={queue} />
          <SystemStrip h={ui.health} st={st} />
        </div>
      </div>
    </>
  );
}

function Panel({ id, title, sub, right, children, className = "" }: { id: string; title: ReactNode; sub?: string; right?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`card panel ${className}`} data-testid={id}>
      <div className="panel-h">
        <div style={{ flex: 1, minWidth: 180 }}>
          <h2>{title}</h2>
          {sub && <small className="muted">{sub}</small>}
        </div>
        {right}
      </div>
      {children}
    </section>
  );
}

function ActiveChats({ st, live, stuck }: { st: State; live: Conversation[]; stuck: Set<string> }) {
  const { data } = st;
  const ago = useAgo();
  const rows = [...live].sort((a, b) => (b.lastAt ?? 0) - (a.lastAt ?? 0)).slice(0, 4);
  return (
    <Panel
      id="overview-active-chats"
      title="Active chats"
      sub="Personas talking to scammers right now"
      right={
        <span className="live">
          <span className="dot" />
          <span className="caps">
            {USE_SAMPLE ? "Sample" : "Live"} · {ago}
          </span>
        </span>
      }
    >
      {rows.length ? (
        <div className="chats">
          {rows.map((c) => {
            const p = data.personas.find((x) => x.id === c.personaId);
            const captured = data.entities.filter((e) => e.convoId === c.id).length;
            const last = c.msgs.at(-1);
            return (
              <Link key={c.id} href={`/conversations/${c.id}`} className="chatcard" data-testid={stuck.has(c.id) ? "overview-attention-stuck" : "overview-chat"}>
                <span className="top">
                  <span className="mono">{c.scammer}</span>
                  <span className="muted">{c.last}</span>
                </span>
                <span className="mid">
                  <b>→ {p?.name ?? "?"}</b>
                  <span className="caps muted">
                    Stage {c.stage + 1} · {STAGES[c.stage] ?? "?"}
                  </span>
                </span>
                {last && <span className="prev">{last.text}</span>}
                <span className="row-gap">
                  <Chip>{c.scam}</Chip>
                  <Badge word={c.status} />
                  {stuck.has(c.id) && <Badge word="stuck over 2 h" tone="warning" />}
                  {captured > 0 && <span className="muted">{captured} captured</span>}
                </span>
              </Link>
            );
          })}
        </div>
      ) : (
        <div className="empty" style={{ padding: "20px 12px" }}>
          <b>No open chats</b>
          <span>Chats appear here when a scammer messages an active persona.</span>
        </div>
      )}
      <Link className="btn btn-quiet btn-sm" href="/conversations" style={{ justifySelf: "start" }}>
        Open Conversations →
      </Link>
    </Panel>
  );
}

// The design's "AI summary · today" card: the AI overview (GET /internal/summary) plus stats and lines
// counted from real data.
function Today({ st }: { st: State }) {
  const { data, ui } = st;
  const ai = useAISummary(USE_SAMPLE ? null : "/internal/summary");
  const ref = ui.lastSync || 0;
  const today = (ms?: number) => ms !== undefined && ref > 0 && sameDay(ms, ref);

  const msgs = data.convos.flatMap((c) => c.msgs.map((m, i) => ({ c, m, prev: c.msgs[i - 1] })));
  const replies = msgs.filter(({ m }) => m.who === "persona" && today(m.at));
  // Reply delay: from a scammer message to the persona's answer, today, where both times are known.
  const gaps = replies.filter(({ prev, m }) => prev?.who === "scammer" && prev.at && m.at && m.at > prev.at).map(({ prev, m }) => (m.at! - prev!.at!) / 1000);
  const delay = gaps.length ? `${Math.round(Math.min(...gaps))}–${Math.round(Math.max(...gaps))} s` : null;
  const captured = data.entities.filter((e) => today(e.firstAt));
  const conf = (["high", "medium", "low"] as const).map((k) => [k, captured.filter((e) => e.conf === k).length] as const).filter(([, n]) => n);
  const chatsToday = data.convos.filter((c) => today(c.lastAt) || c.msgs.some((m) => today(m.at))).length;

  const stats: [string, string | null][] = [
    ["Persona replies", replies.length || msgs.some(({ m }) => m.at) ? String(replies.length) : null],
    ["Reply delay", delay],
    ["Auto-extracted", captured.length ? `${captured.length}${conf.length ? " · " + conf.map(([k, n]) => `${n} ${k}`).join(", ") : ""}` : msgs.some(({ m }) => m.at) ? "0" : null],
    ["Chats today", msgs.some(({ m }) => m.at) ? String(chatsToday) : null],
  ];
  const shown = stats.filter(([, v]) => v !== null) as [string, string][];

  // What happened: one line per money account captured today, then personas in several live chats.
  const lines: { ok: boolean; text: ReactNode }[] = [];
  for (const e of captured.filter((x) => x.type === "bank_account" || x.type === "wallet").slice(0, 3)) {
    const c = e.convoId ? data.convos.find((x) => x.id === e.convoId) : undefined;
    const p = c && data.personas.find((x) => x.id === c.personaId);
    const what = (
      <>
        {e.type === "bank_account" ? (BANKS[e.bank] ?? e.bank) : `${e.bank} wallet`} <span className="mono">{showVal(e)}</span>
      </>
    );
    lines.push({
      ok: e.status === "verified",
      text: c ? (
        <>
          <span className="mono">{c.scammer}</span> sent {what}
          {p ? ` to ${p.name}` : ""}
          {e.firstAt ? ` at ${clockOf(e.firstAt)}` : ""}.{e.status === "verified" ? " Verified." : " Waiting for a human."}
        </>
      ) : (
        <>
          Captured {what}
          {e.firstAt ? ` at ${clockOf(e.firstAt)}` : ""}.{e.status === "verified" ? " Verified." : " Waiting for a human."}
        </>
      ),
    });
  }
  const perPersona = new Map<string, number>();
  liveConvos(data).forEach((c) => perPersona.set(c.personaId, (perPersona.get(c.personaId) ?? 0) + 1));
  for (const [pid, n] of perPersona) {
    const p = data.personas.find((x) => x.id === pid);
    if (p && n > 1) lines.push({ ok: true, text: `${p.name} is in ${n} live chats.` });
  }
  const waiting = queueOf(data).length;
  if (waiting) lines.push({ ok: false, text: `${plural(waiting, "capture")} wait${waiting === 1 ? "s" : ""} for a human. Auto-extraction stops at medium, so banks see none of them until you verify.` });

  return (
    <Panel
      id="overview-today"
      className="today"
      title={<span className="caps muted">Summary · today</span>}
      right={<Badge word={USE_SAMPLE ? "sample" : "live"} />}
    >
      {shown.length > 0 && (
        <div className="today-stats">
          {shown.map(([k, v]) => (
            <div key={k}>
              <span className="caps muted">{k}</span>
              <b className={k === "Reply delay" ? "mono" : undefined}>{v}</b>
            </div>
          ))}
        </div>
      )}
      {!USE_SAMPLE && (
        <div className="today-what" data-testid="overview-ai-summary" aria-busy={ai.loading}>
          <span className="caps muted">AI summary</span>
          {ai.summary ? (
            <p data-testid="overview-ai-summary-text">{ai.summary.summary}</p>
          ) : ai.loading ? (
            <span className="muted">
              <Spin /> Writing today&apos;s summary…
            </span>
          ) : (
            <span className="muted">{ai.error ? `AI summary unavailable: ${ai.error}` : "No summary yet."}</span>
          )}
          {ai.summary && (
            <small className="muted" data-testid="overview-ai-summary-meta">
              {ai.summary.model.replace(/^anthropic\//, "")} · {summaryTime(ai.summary.created_at)}
              {ai.loading ? " · updating…" : ""}{" "}
              <button type="button" className="btn btn-quiet btn-sm" onClick={ai.regenerate} disabled={ai.loading}>
                Refresh
              </button>
            </small>
          )}
        </div>
      )}
      <div className="today-what">
        <span className="caps muted">What happened</span>
        {lines.length ? (
          <ul>
            {lines.slice(0, 5).map((l, i) => (
              <li key={i}>
                <span className={`tick ${l.ok ? "y" : "n"}`}>{l.ok ? "✓" : "!"}</span>
                <span>{l.text}</span>
              </li>
            ))}
          </ul>
        ) : (
          <span className="muted">Nothing yet today.</span>
        )}
      </div>
      <small className="muted">AI summary written by Claude from the live chats; the stats and lines below are counted from today&apos;s chats and captures.</small>
    </Panel>
  );
}

function PersonaCards({ personas, convos }: { personas: Persona[]; convos: Conversation[] }) {
  const order = { active: 0, draft: 1, paused: 2, retired: 3 } as const;
  const list = [...personas].sort((a, b) => order[a.status] - order[b.status]);
  return (
    <section className="ov-personas" aria-label="Personas" data-testid="overview-personas">
      <div className="panel-h">
        <div style={{ flex: 1 }}>
          <span className="caps muted">Personas</span>
          <h2 className="ov-h2">Who scammers are talking to</h2>
        </div>
        <Link className="btn btn-secondary btn-sm" href="/personas">
          Manage personas
        </Link>
      </div>
      {list.length ? (
        <div className="pcards">
          {list.map((p) => {
            const n = convos.filter((c) => c.personaId === p.id && c.status !== "closed").length;
            return (
              <div key={p.id} className="card pcard" data-testid="overview-persona-card">
                <div className="namecell">
                  <Avatar p={p} lg />
                  <div>
                    <b>
                      {p.name}
                      {p.age ? `, ${p.age}` : ""}
                    </b>
                    <small>{[p.loc !== "—" ? p.loc : null, p.occ || null].filter(Boolean).join(" · ")}</small>
                  </div>
                </div>
                <span className="row-gap">
                  <Badge word={p.status} />
                  <span className="mono muted" style={{ fontSize: 14 }}>
                    {p.handle}
                  </span>
                </span>
                <span className="muted">{n ? plural(n, "live chat") : "No chats yet"}</span>
                <span className="row-gap" style={{ gap: 14 }}>
                  <Link className="btn btn-quiet btn-sm" href={`/personas?selected=${encodeURIComponent(p.id)}`}>
                    Profile →
                  </Link>
                  {DEV_CHAT && p.status === "active" && (
                    <Link className="btn btn-quiet btn-sm" href={`/demo/chat?persona=${encodeURIComponent(p.id)}`}>
                      Chat as scammer →
                    </Link>
                  )}
                </span>
              </div>
            );
          })}
        </div>
      ) : (
        <div className="card empty">
          <b>No personas yet</b>
          <Link className="btn btn-secondary btn-sm" href="/personas">
            Go to Personas
          </Link>
        </div>
      )}
    </section>
  );
}

function NextInQueue({ queue }: { queue: Entity[] }) {
  // Waiting longest first. Rows without a timestamp (seed data) keep the queue's order, ahead of timed ones.
  const oldest = [...queue].sort((a, b) => (a.firstAt ?? 0) - (b.firstAt ?? 0)).slice(0, 4);
  return (
    <Panel
      id="overview-attention"
      title={<span className="caps muted">Next in the Verify queue</span>}
      right={
        <Link className="btn btn-quiet btn-sm" href="/verify">
          All {queue.length} →
        </Link>
      }
    >
      {oldest.length ? (
        <ul className="rows">
          {oldest.map((e) => (
            <li key={e.id} data-testid="overview-attention-entity">
              <div className="grow">
                <span className="caps muted">{typeLabel(e)}</span>
                <span className="mono" style={{ overflowWrap: "anywhere" }}>
                  {showVal(e)}
                </span>
                <small>First seen {e.first}</small>
              </div>
              <span className="q-acts">
                <Badge word={e.conf} />
                <Link className="btn btn-quiet btn-sm" href={`/verify?selected=${e.id}`}>
                  Review
                </Link>
                {/* Same Verify as the queue: acts at once, saves after the 6 s Undo window (spec §4.5). */}
                <button className="btn btn-success btn-sm" onClick={() => decide(e.id, "verified")} data-testid="overview-verify-btn">
                  Verify
                </button>
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <div className="empty" style={{ padding: "20px 12px" }}>
          <b>Nothing needs a human right now</b>
          <span>New captures land in the Verify queue the moment a scammer sends one.</span>
        </div>
      )}
    </Panel>
  );
}

// GET /readyz (every 10 s) plus the honeypot accounts. Sample mode runs no health check, so it says so.
function SystemStrip({ h, st }: { h: Health | null; st: State }) {
  const tg = st.ui.accounts.filter((a) => a.channel === "telegram" && !isSimAccount(a));
  const tgActive = tg.filter((a) => a.status === "active").length;
  let tone: "ok" | "bad" | "" = "";
  let head: ReactNode;
  let body: string;
  if (USE_SAMPLE) {
    head = "Health checks don't run in sample mode";
    body = "Switch sample mode off to see the real backend.";
  } else if (!h) {
    head = (
      <>
        <Spin /> Checking backend…
      </>
    );
    body = "";
  } else {
    tone = h.ok ? "ok" : "bad";
    const checks = Object.entries(h.checks).map(([k, v]) => `${CHECK_NAMES[k] ?? k} ${v}`);
    head = `${h.ok ? "Backend up" : h.status ? "Backend not ready" : "Backend unreachable"} · ${tg.length ? `Telegram: ${tgActive} of ${tg.length} accounts active` : "Telegram has no accounts bound yet"}`;
    body = [checks.join(" · "), `${h.ms} ms`, !tgActive ? "Web chat still works for the demo." : ""].filter(Boolean).join(" · ");
  }
  return (
    <section className={`card panel sys-strip ${tone}`} data-testid="overview-health" role={tone === "bad" ? "alert" : "status"}>
      <span className="caps muted">System</span>
      <b>{head}</b>
      {body && <small className="muted">{body}</small>}
      <Link className="btn btn-quiet btn-sm" href="/status" style={{ justifySelf: "start" }}>
        Open Status for every check →
      </Link>
    </section>
  );
}
