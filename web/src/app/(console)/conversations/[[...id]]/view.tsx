"use client";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useState } from "react";
import { Bubble, EntityCard, StageBar } from "@/components/chat";
import { Avatar, Badge, Chip, Head, Typing } from "@/components/ui";
import { DEV_CHAT, useAgo, useQuery, useStickToBottom } from "@/lib/hooks";
import { handback, operatorSend, takeover, useStore } from "@/lib/store";
import { summaryTime, useAISummary, useStageHistory } from "@/lib/summary";
import { USE_SAMPLE } from "@/lib/store";
import type { Conversation } from "@/lib/types";

// "active" = still running (bot or operator); it is what the page opens on.
const FILTERS: [string, string][] = [
  ["active", "Active"],
  ["all", "All"],
  ["bot", "Bot"],
  ["operator", "Operator"],
  ["closed", "Closed"],
];

export function ConversationsView() {
  const { data } = useStore();
  const q = useQuery();
  const ago = useAgo();
  const params = useParams<{ id?: string[] }>();
  const f = q.get("status", "active");
  const list = data.convos.filter((c) => f === "all" || (f === "active" ? c.status !== "closed" : c.status === f));
  // No id in the URL: open the first chat of the current tab, so Active never opens a closed chat.
  const c = data.convos.find((x) => x.id === params.id?.[0]) ?? list[0];
  const qs = f === "active" ? "" : `?status=${f}`;

  return (
    <>
      <Head
        title="Conversations"
        sub="Watch personas talk to scammers. Take over any time."
        right={
          <span className="live">
            <span className="dot" />
            <span className="caps">Live · updated {ago}</span>
          </span>
        }
      />
      <div className="split">
        <div className="list">
          <div className="tabs" role="group" aria-label="Filter by status">
            {FILTERS.map(([k, l]) => (
              <button key={k} aria-pressed={f === k} onClick={() => q.set({ status: k === "active" ? null : k }, "/conversations")}>
                {l}
              </button>
            ))}
          </div>
          {list.length ? (
            list.map((x) => {
              const p = data.personas.find((y) => y.id === x.personaId);
              return (
                <Link
                  key={x.id}
                  href={`/conversations/${x.id}${qs}`}
                  replace
                  scroll={false}
                  className="item crow"
                  aria-current={x.id === c?.id}
                  title={`${x.scammer} → ${p?.name ?? "?"} · ${x.status}`}
                  data-testid="convo-row"
                  style={{ color: "inherit", textDecoration: "none" }}
                >
                  <span className={`sdot s-${x.status}`} data-testid="convo-status">
                    <span className="sr-only">{x.status}</span>
                  </span>
                  <span className="cname">{p?.name ?? "?"}</span>
                  <span className="t">{x.last}</span>
                </Link>
              );
            })
          ) : (
            <div className="card empty">
              <b>Nothing here</b>
              <span>{f === "all" ? "No conversations yet." : `No ${(FILTERS.find(([k]) => k === f)?.[1] ?? f).toLowerCase()} conversations right now.`}</span>
            </div>
          )}
        </div>
        {c ? (
          <ChatPane key={c.id} c={c} />
        ) : (
          <div className="card empty" style={{ minHeight: 400, alignContent: "center" }}>
            <b>No conversation selected</b>
            <span>Pick one on the left{DEV_CHAT ? ", or start one from Web chat" : ""}.</span>
            {DEV_CHAT && (
              <Link className="btn btn-primary" href="/demo/chat">
                Open web chat →
              </Link>
            )}
          </div>
        )}
      </div>
    </>
  );
}

function ChatPane({ c }: { c: Conversation }) {
  const { data, ui } = useStore();
  const [draft, setDraft] = useState("");
  const p = data.personas.find((x) => x.id === c.personaId);
  const ents = data.entities.filter((e) => e.convoId === c.id);
  const typing = ui.typing[c.id];
  const ref = useStickToBottom(`${c.msgs.length}-${ents.length}-${typing}`);
  // Stage history and AI summary re-check when the stage changes; a missing summary is generated, a
  // stale one is left to the backend refresh (stage change / every few messages) or the Refresh button.
  const reached = useStageHistory(USE_SAMPLE ? null : c.id, c.stage);
  const ai = useAISummary(USE_SAMPLE || !c.msgs.length ? null : `/internal/conversations/${encodeURIComponent(c.id)}/summary`, `${c.stage}-${c.msgs.length > 0}`, false);
  if (!p) return null;
  const isOp = c.status === "operator";
  const closed = c.status === "closed";

  let footer;
  if (closed) {
    footer = (
      <div className="chat-f">
        <span className="txt">Conversation closed. Its entities stay in the intelligence database.</span>
      </div>
    );
  } else if (isOp) {
    footer = (
      <form
        className="chat-f op"
        onSubmit={(ev) => {
          ev.preventDefault();
          const t = draft.trim();
          if (!t) return;
          operatorSend(c.id, t);
          setDraft("");
        }}
      >
        <span className="badge t-operator">You are replying as {p.name}</span>
        <div className="composer">
          <input
            className="input"
            autoComplete="off"
            placeholder={`Type as ${p.name}…`}
            value={draft}
            onChange={(ev) => setDraft(ev.target.value)}
            data-testid="operator-input"
            aria-label="Operator message"
            autoFocus
          />
          <button className="btn btn-primary" data-testid="operator-send-btn">
            Send
          </button>
        </div>
        <button type="button" className="btn btn-secondary" onClick={() => handback(c.id)}>
          Hand back to bot
        </button>
      </form>
    );
  } else {
    footer = (
      <div className="chat-f">
        <span className="txt">Bot is replying with a 10–30 s human delay. Take over any time. The bot stops instantly.</span>
        <button className="btn btn-secondary" onClick={() => takeover(c.id)} data-testid="takeover-btn">
          Take over
        </button>
      </div>
    );
  }

  return (
    <section className="card chat" aria-label="Conversation">
      <div className="chat-h">
        <Avatar p={p} lg />
        <div className="who">
          <b className="mono">{c.scammer}</b>{" "}
          <b>
            → {p.name}, {p.age} · {p.loc}
          </b>
          <small>
            {c.web ? "Web chat" : "Telegram"} · honeypot {p.handle}
          </small>
        </div>
        <Chip>{c.scam}</Chip>
        <Badge word={isOp ? "operator" : closed ? "closed" : "bot replying"} tone={isOp ? "operator" : closed ? "neutral" : "info"} />
      </div>
      <StageBar c={c} reached={reached} />
      {!USE_SAMPLE && c.msgs.length > 0 && (
        <div className="chat-sum" data-testid="convo-ai-summary" aria-busy={ai.loading}>
          <span className="caps muted">AI summary</span>
          {ai.summary ? (
            <p data-testid="convo-ai-summary-text">{ai.summary.summary}</p>
          ) : (
            <span className="muted">{ai.loading ? "Writing a summary…" : ai.error ? `AI summary unavailable: ${ai.error}` : "No summary yet."}</span>
          )}
          {ai.summary && (
            <small className="muted">
              {ai.summary.model.replace(/^anthropic\//, "")} · {summaryTime(ai.summary.created_at)}
              {ai.stale ? " · older than the latest messages" : ""}
              {ai.loading ? " · updating…" : ""}{" "}
              <button type="button" className="btn btn-quiet btn-sm" onClick={ai.regenerate} disabled={ai.loading} data-testid="convo-ai-summary-refresh">
                Refresh
              </button>
            </small>
          )}
        </div>
      )}
      <div className="msgs" ref={ref}>
        {c.msgs.map((m, i) => (
          <Bubble key={i} m={m} />
        ))}
        {ents.map((e) => (
          <EntityCard key={e.id} e={e} />
        ))}
        {typing && <Typing name={p.name} />}
      </div>
      {footer}
    </section>
  );
}
