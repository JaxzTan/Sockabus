"use client";
import type { ReactNode } from "react";
import { Badge, Head, type Tone } from "@/components/ui";
import { DEV_CHAT, useAgo } from "@/lib/hooks";
import { plural } from "@/lib/format";
import { isSimAccount } from "@/lib/api";
import { activePersonas, liveConvos, toast, toggleTelegram, USE_SAMPLE, useStore, type State } from "@/lib/store";

function Tile({ id, label, big, sub, word, tone, bad }: { id: string; label: string; big: ReactNode; sub: string; word: string; tone: Tone; bad?: boolean }) {
  return (
    <div className={`card tile${bad ? " bad" : ""}`} data-testid={id}>
      <div className="th">
        <span className="caps">{label}</span>
        <Badge word={word} tone={tone} />
      </div>
      <div className="v">{big}</div>
      <small>{sub}</small>
    </div>
  );
}

export function StatusView() {
  const st = useStore();
  const ago = useAgo();
  if (!USE_SAMPLE) return <LiveStatus st={st} ago={ago} />;
  const { data } = st;
  const ok = data.telegram;
  return (
    <>
      <Head
        title="Status"
        sub="Proof the honeypot listens 24/7, and that we know within 2 minutes if it stops."
        right={
          <button className="btn btn-secondary" onClick={() => toast("This will open status.sockabus.my (Uptime Kuma) once it is deployed")}>
            Open uptime monitor ↗
          </button>
        }
      />
      <div className={`overall ${ok ? "ok" : "bad"}`} role="status">
        <span className="ic">{ok ? "✓" : "!"}</span>
        <div className="txt">
          <h2>{ok ? "All systems operational" : "Degraded: Telegram disconnected"}</h2>
          <span className="muted" style={{ fontSize: 15 }}>
            {ok ? `Auto-refresh every 10 s · last checked ${ago}` : "Team alerted by Telegram group and email. Demo can continue on Web chat."}
          </span>
        </div>
        {/* Prototype control: remove in production (spec S6). */}
        {DEV_CHAT && (
          <button className="btn btn-secondary btn-sm" onClick={toggleTelegram}>
            {ok ? "Simulate: Telegram drops" : "Simulate: reconnect"}
          </button>
        )}
      </div>
      <div className="tiles">
        <Tile
          id="status-telegram"
          label="Telegram honeypot"
          big={ok ? "Connected" : "Disconnected"}
          sub={ok ? `${activePersonas(data).length} accounts active · heartbeat every 60 s` : "No heartbeat for 2 min · alert sent"}
          word={ok ? "up" : "down"}
          tone={ok ? "success" : "danger"}
          bad={!ok}
        />
        <Tile
          id="status-last-message"
          label="Last message received"
          big={ok ? ago : "2 min ago"}
          sub={ok ? "From the most recent scammer chat" : "Nothing received since the drop"}
          word={ok ? "live" : "stale"}
          tone={ok ? "success" : "warning"}
        />
        <Tile id="status-llm-errors" label="AI model" big="0 errors" sub="Gemini · last 15 minutes · fallback: Claude" word="ok" tone="success" />
        <Tile id="status-workflows" label="Conversations running" big={String(liveConvos(data).length)} sub="Capacity 1,000 · none stuck over 2 h" word="running" tone="info" />
        <Tile id="status-api" label="Lookup API" big="41 ms" sub="Synthetic check every 60 s returns a match as expected" word="up" tone="success" />
        <Tile id="status-health" label="Backend health" big="200 OK" sub="Database, Redis and Temporal reachable" word="up" tone="success" />
      </div>
      <div className="lower">
        <div className="card uptime">
          <div className="row-gap" style={{ justifyContent: "space-between" }}>
            <span className="caps muted">Last 24 hours</span>
            <b style={{ color: "var(--green)" }}>99.9% up</b>
          </div>
          <div className="bars" aria-label="Hourly uptime, last 24 hours">
            {Array.from({ length: 24 }, (_, i) => (
              <i key={i} className={i === 9 ? "a" : i === 23 && !ok ? "r" : ""} title={`${i}:00`} />
            ))}
          </div>
          <span className="muted" style={{ fontSize: 15 }}>
            One amber hour at 09:00: the bot restarted after a deploy and reconnected in 40 s. Amber means degraded, red means down.
          </span>
        </div>
        <div className="alerting">
          <span className="caps">If something stops</span>
          <h3>The team gets a Telegram and email alert within 2 minutes.</h3>
          <p>Last drill: bot stopped on purpose at 15:10, alert arrived in 1 min 38 s, recovery alert on restart.</p>
        </div>
      </div>
    </>
  );
}

// "14:32" today, "31 Oct 14:32" otherwise.
const stamp = (iso: string) => {
  const d = new Date(iso);
  const time = d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
  return d.toDateString() === new Date().toDateString() ? time : `${d.toLocaleDateString("en-GB", { day: "numeric", month: "short" })} ${time}`;
};

// API mode: tiles from GET /readyz, /internal/status, /internal/accounts and /internal/conversations.
// Lookup latency and uptime history live in the uptime monitor, not here.
function LiveStatus({ st, ago }: { st: State; ago: string }) {
  const { data, ui } = st;
  const h = ui.health;
  const bot = ui.bot;
  const tg = ui.accounts.filter((a) => a.channel === "telegram" && !isSimAccount(a));
  const tgActive = tg.filter((a) => a.status === "active").length;
  const lastAt = data.convos.map((c) => c.last).at(0);
  // Accounts are bound but none holds a live Telegram session: the honeypot hears nothing.
  const tgDown = !!bot && tgActive > 0 && !bot.telegram.connected;
  const healthy = !!h?.ok && !ui.loadError && !tgDown;
  const llmErrors = bot?.llm.errors_recent ?? 0;
  const running = bot?.workflows.running;
  const checks = h ? Object.entries(h.checks) : [];
  const failing = checks.filter(([, v]) => v !== "ok").map(([k]) => k);
  // Backend down before the first load: no data, so say so instead of showing zeros.
  const none = !ui.loaded;
  return (
    <>
      <Head title="Status" sub="Proof the honeypot listens 24/7, and that we know within 2 minutes if it stops." />
      <div className={`overall ${healthy ? "ok" : "bad"}`} role="status">
        <span className="ic">{healthy ? "✓" : "!"}</span>
        <div className="txt">
          <h2>{!h ? "Checking…" : healthy ? "All systems operational" : tgDown && h.ok ? "Degraded: Telegram disconnected" : "Degraded: backend not ready"}</h2>
          <span className="muted" style={{ fontSize: 15 }}>
            Auto-refresh every 10 s · data updated {ago}
          </span>
        </div>
      </div>
      <div className="tiles">
        {bot ? (
          <Tile
            id="status-telegram"
            label="Telegram honeypot"
            big={bot.telegram.connected ? "Connected" : tg.length ? "Disconnected" : "No accounts"}
            sub={
              bot.telegram.connected
                ? `${tgActive} of ${tg.length} accounts active · heartbeat every 60 s`
                : tg.length
                  ? "No live Telegram session · the demo can continue on Web chat"
                  : "Bind one with make tglogin. Web chat still works."
            }
            word={bot.telegram.connected ? "up" : tg.length ? "down" : "none"}
            tone={bot.telegram.connected ? "success" : tg.length ? "danger" : "warning"}
            bad={tgDown}
          />
        ) : (
          <Tile
            id="status-telegram"
            label="Telegram honeypot"
            big={none ? "Unknown" : tg.length ? `${tgActive} of ${tg.length} active` : "No accounts"}
            sub={none ? "No data from the backend yet" : "Account status from the database · live connection state unavailable"}
            word={none ? "n/a" : tgActive ? "up" : "none"}
            tone={none ? "neutral" : tgActive ? "success" : "warning"}
            bad={!none && !tgActive}
          />
        )}
        {bot ? (
          <Tile
            id="status-last-message"
            label="Last message received"
            big={bot.telegram.last_message_at ? stamp(bot.telegram.last_message_at) : "None yet"}
            sub="Last private message on Telegram since the backend started"
            word={bot.telegram.last_message_at ? "live" : "idle"}
            tone={bot.telegram.last_message_at ? "success" : "neutral"}
          />
        ) : (
          <Tile
            id="status-last-message"
            label="Last message received"
            big={none ? "Unknown" : (lastAt ?? "None yet")}
            sub={none ? "No data from the backend yet" : "From the most recent scammer chat"}
            word={none ? "n/a" : lastAt ? "live" : "idle"}
            tone={!none && lastAt ? "success" : "neutral"}
          />
        )}
        <Tile
          id="status-llm-errors"
          label="AI model"
          big={bot ? plural(llmErrors, "error") : "Unknown"}
          sub={bot ? "Failed calls after retries · last 15 minutes" : "Status endpoint unreachable"}
          word={!bot ? "n/a" : llmErrors ? "errors" : "ok"}
          tone={!bot ? "neutral" : llmErrors >= 5 ? "danger" : llmErrors ? "warning" : "success"}
          bad={llmErrors >= 5}
        />
        <Tile
          id="status-workflows"
          label="Conversations running"
          big={bot?.workflows.error ? "Unknown" : running != null ? String(running) : none ? "Unknown" : String(liveConvos(data).length)}
          sub={bot?.workflows.error ? "Temporal unavailable" : running != null ? "Temporal workflows running" : none ? "No data from the backend yet" : `${activePersonas(data).length} personas active`}
          word={bot?.workflows.error ? "down" : none && running == null ? "n/a" : "running"}
          tone={bot?.workflows.error ? "danger" : none && running == null ? "neutral" : "info"}
          bad={!!bot?.workflows.error}
        />
        <Tile id="status-api" label="Lookup API" big="On demand" sub="Try it on the Bank simulator, which shows the real time taken" word="manual" tone="neutral" />
        <Tile
          id="status-health"
          label="Backend health"
          big={h ? (h.status ? `${h.status} ${h.ok ? "OK" : "not ready"}` : "Unreachable") : "…"}
          sub={h ? (failing.length ? `Failing: ${failing.join(", ")}` : `${checks.map(([k]) => k).join(", ") || "No checks"} reachable · ${h.ms} ms`) : "Checking /readyz"}
          word={h?.ok ? "up" : "down"}
          tone={h?.ok ? "success" : "danger"}
          bad={!!h && !h.ok}
        />
      </div>
      <div className="lower">
        <div className="card uptime">
          <span className="caps muted">Last 24 hours</span>
          <span className="muted" style={{ fontSize: 15 }}>
            Uptime history comes from the uptime monitor (Uptime Kuma), which is not connected yet.
          </span>
        </div>
        <div className="alerting">
          <span className="caps">If something stops</span>
          <h3>The team gets a Telegram and email alert within 2 minutes.</h3>
          <p>Alerting runs in the uptime monitor, outside this console.</p>
        </div>
      </div>
    </>
  );
}
