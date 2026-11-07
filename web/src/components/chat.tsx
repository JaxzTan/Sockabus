"use client";
import Link from "next/link";
import { Fragment } from "react";
import { Badge } from "@/components/ui";
import { plural, showVal, STAGES, typeLabel } from "@/lib/format";
import { decide } from "@/lib/store";
import { bankHref } from "@/components/verified";
import { summaryTime, type ApiStageEvent } from "@/lib/summary";
import type { Conversation, Entity, Message } from "@/lib/types";

const STAGE_KEYS = ["rapport", "bait", "payment_request", "stall"];

// StageBar shows the playbook stage. With `reached` (GET /internal/conversations/{id}/stages) each
// stage that was reached carries its time and source in the tooltip.
export function StageBar({ c, reached }: { c: Conversation; reached?: Record<string, ApiStageEvent> }) {
  return (
    <div className="stages" aria-label="Playbook stage" data-testid="stage-bar">
      {STAGES.map((s, i) => {
        const cls = i < c.stage ? "done" : i === c.stage ? "now" : "";
        const ev = reached?.[STAGE_KEYS[i]];
        return (
          <Fragment key={s}>
            <span
              className={`stage ${cls}`}
              data-testid="stage-step"
              data-stage={STAGE_KEYS[i]}
              data-reached-at={ev?.created_at}
              title={ev ? `${s}: reached ${summaryTime(ev.created_at)} (${ev.source})` : undefined}
            >
              <b>{i < c.stage ? "✓" : i + 1}</b>
              {s}
            </span>
            {i < STAGES.length - 1 && <hr className={i < c.stage ? "done" : ""} />}
          </Fragment>
        );
      })}
    </div>
  );
}

export function Bubble({ m }: { m: Message }) {
  if (m.undecryptable)
    return (
      <div className={`msg ${m.who} undecryptable`} data-testid="chat-message" data-undecryptable="true">
        <p>Message could not be decrypted</p>
        <small data-testid="chat-message-sender">
          {m.who} · {m.t}
        </small>
      </div>
    );
  return (
    <div className={`msg ${m.who}`} data-testid="chat-message">
      <p>{m.text}</p>
      <small data-testid="chat-message-sender">
        {m.who} · {m.t}
      </small>
    </div>
  );
}

// Inline "entity extracted" card: verify right inside the chat, no page switch (F17).
export function EntityCard({ e }: { e: Entity }) {
  if (e.status === "verified")
    return (
      <div className="ent ok">
        <div className="txt">
          <span className="caps" style={{ color: "var(--green)" }}>
            Verified · {typeLabel(e)}
          </span>
          <div className="val">
            <span className="mono">{showVal(e)}</span>
            <Badge word="verified" />
            <Badge word="high" />
          </div>
          <p>Banks checking this account now get a match with high confidence.</p>
        </div>
        {e.type === "bank_account" && (
          <Link className="btn btn-secondary btn-sm" href={bankHref(e.id)} data-testid="bank-test-link">
            Test in bank simulator →
          </Link>
        )}
      </div>
    );
  if (e.status === "rejected")
    return (
      <div className="ent no">
        <div className="txt">
          <span className="caps muted">Rejected · {typeLabel(e)}</span>
          <div className="val">
            <span className="mono">{showVal(e)}</span>
            <Badge word="rejected" />
          </div>
          <p>Kept for the audit trail. Never returned as high confidence.</p>
        </div>
      </div>
    );
  return (
    <div className="ent">
      <div className="txt">
        <span className="caps" style={{ color: "var(--amber)" }}>
          Entity extracted · {typeLabel(e)} · from this chat
        </span>
        <div className="val">
          <span className="mono">{showVal(e)}</span>
          <Badge word="unverified" />
          <Badge word={e.conf} />
          <span className="muted" style={{ fontSize: 15 }}>
            {plural(e.sightings, "sighting")}
          </span>
        </div>
        <p>Auto-extraction stops at medium. Verify here or in the queue. No page switch needed.</p>
      </div>
      <div className="row-gap">
        <button className="btn btn-secondary btn-sm" onClick={() => decide(e.id, "rejected")}>
          Reject
        </button>
        <button className="btn btn-success btn-sm" onClick={() => decide(e.id, "verified")} data-testid="verify-btn">
          Verify
        </button>
      </div>
    </div>
  );
}
