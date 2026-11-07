"use client";
// DemoBank's mock fraud console: outcome, review queue for held transfers and the intel feed. A
// real bank keeps its own queue; this one lives in the browser (lib/bankDemo). Recommend, don't
// auto-freeze: every action here is a person's decision.
import { Badge } from "@/components/ui";
import { stamp } from "@/lib/api";
import { decideHold, prevented, reviewMule, useBankDemo, type FlaggedTransfer } from "@/lib/bankDemo";
import { STORY_ACCOUNT, STORY_BANK_NAME, STORY_REPORT_DELAY_MIN } from "@/lib/bankStory";
import { BANKS, maskDigits } from "@/lib/format";
import { ACTION_LABEL } from "@/lib/risk";
import { useStore } from "@/lib/store";
import { minutesAgo, useNow } from "./story";

const bankName = (code: string) => (code === "DEMO" ? STORY_BANK_NAME : (BANKS[code] ?? code));
const rm = (n: number) => `RM ${n.toLocaleString("en-MY", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;

export function FraudConsole() {
  const s = useBankDemo();
  const { data } = useStore();
  const now = useNow();
  const saved = prevented(s);
  const storyT = s.transfers.find((t) => t.story);
  const pending = s.transfers.filter((t) => t.decision === "pending");
  const decided = s.transfers.filter((t) => t.decision !== "pending").slice(0, 4);
  // Intel feed: the story's account plus the newest real captures, always masked.
  const real = data.entities.filter((e) => e.type === "bank_account" && e.status !== "rejected").slice(0, 4);

  return (
    <section className="card fconsole" aria-label="DemoBank fraud console" data-testid="bank-console">
      <div className="fc-h">
        <b>DemoBank fraud console</b>
        <Badge word="mock" tone="neutral" />
      </div>

      <div className="outcome" data-testid="bank-outcome">
        <div>
          <span className="caps muted">Prevented</span>
          <b data-testid="outcome-prevented">{rm(saved)}</b>
          <small>holds a reviewer confirmed</small>
        </div>
        <div>
          <span className="caps muted">Head start</span>
          {storyT ? (
            <>
              <b data-testid="outcome-lead">{STORY_REPORT_DELAY_MIN} min</b>
              <small>flagged before the first victim report (scripted)</small>
            </>
          ) : (
            <>
              <b>—</b>
              <small>play the story to see it</small>
            </>
          )}
        </div>
      </div>

      <div className="fc-sec">
        <span className="caps muted">Review queue · {pending.length}</span>
        {pending.length === 0 && <p className="muted fc-empty">No transfers waiting. Held transfers land here for a person to decide.</p>}
        {pending.map((t) => (
          <QueueItem key={t.id} t={t} now={now} />
        ))}
        {decided.map((t) => (
          <QueueItem key={t.id} t={t} now={now} />
        ))}
      </div>

      <div className="fc-sec">
        <span className="caps muted">Intel feed from Sockabus</span>
        <ul className="feed" data-testid="bank-feed">
          {s.flaggedAt !== null && (
            <li>
              <span className="mono">
                {STORY_BANK_NAME} {maskDigits(STORY_ACCOUNT)}
              </span>
              <small>
                investment · verified · {minutesAgo(s.flaggedAt, now)} · <i>scripted</i>
              </small>
            </li>
          )}
          {real.map((e) => (
            <li key={e.id}>
              <span className="mono">
                {bankName(e.bank)} {maskDigits(e.value)}
              </span>
              <small>
                {e.scam} · {e.status} · {e.first}
              </small>
            </li>
          ))}
          {s.flaggedAt === null && real.length === 0 && <li className="muted">No flagged accounts yet.</li>}
        </ul>
      </div>
    </section>
  );
}

function QueueItem({ t, now }: { t: FlaggedTransfer; now: number }) {
  const decided = t.decision !== "pending";
  return (
    <div className={`qitem${decided ? " decided" : ""}`} data-testid="queue-item">
      <div className="qi-top">
        <b>{rm(t.amt)}</b>
        <span className="mono">
          → {bankName(t.bank)} {t.acctMasked}
        </span>
        {t.story && <small className="muted">story</small>}
      </div>
      <small className="muted">
        Score {t.score.toFixed(2)} · {ACTION_LABEL[t.action]} · {minutesAgo(t.at, now)}
        {decided ? ` · ${t.decision === "confirmed" ? "hold confirmed" : "released to customer"}` : ""}
      </small>
      {!decided && (
        <div className="row-gap">
          <button type="button" className="btn btn-danger btn-sm" onClick={() => decideHold(t.id, "confirmed")} data-testid="hold-confirm">
            Confirm hold
          </button>
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => decideHold(t.id, "released")} data-testid="hold-release">
            Release to customer
          </button>
        </div>
      )}
      {t.sameBank && (
        <div className="qi-mule">
          <small>Receiving account is at {STORY_BANK_NAME} too: the receiving side can act on it.</small>
          {t.mule === "none" ? (
            <div className="row-gap">
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => reviewMule(t.id, "freeze_requested")} data-testid="mule-freeze">
                Review mule account / request freeze
              </button>
              <button type="button" className="btn btn-quiet btn-sm" onClick={() => reviewMule(t.id, "cleared")}>
                Clear
              </button>
            </div>
          ) : (
            <small data-testid="mule-status">
              <b>{t.mule === "freeze_requested" ? "Freeze requested · sent to account review" : "Cleared by reviewer"}</b>
            </small>
          )}
        </div>
      )}
    </div>
  );
}

// "First seen" on a held result, with "x min ago" for recent records.
export function seenLabel(iso: string | undefined, now: number) {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (!Number.isNaN(t) && now - t < 6 * 3600_000) return `${stamp(iso)} · ${minutesAgo(t, now)}`;
  return stamp(iso);
}
