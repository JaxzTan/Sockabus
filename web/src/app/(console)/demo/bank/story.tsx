"use client";
// The scripted six-step story at the top of the bank simulator (demo spec). Mock data only.
import { useEffect, useState } from "react";
import { flagStoryAccount, markPublicChecked, resetBankDemo, useBankDemo } from "@/lib/bankDemo";
import {
  STORY_ACCOUNT,
  STORY_AMOUNT,
  STORY_BANK,
  STORY_BANK_NAME,
  STORY_CHAT,
  STORY_EVIDENCE_LINE,
  STORY_PERSONA,
  STORY_REPORT_DELAY_MIN,
  STORY_SCAMMER,
} from "@/lib/bankStory";
import { maskDigits } from "@/lib/format";
import { clearLookup, fillBank, setBankDraft } from "@/lib/store";

const MASKED = `${STORY_BANK_NAME} ${maskDigits(STORY_ACCOUNT)}`;

export function minutesAgo(at: number, now: number) {
  const m = Math.max(0, Math.round((now - at) / 60000));
  return m === 0 ? "just now" : `${m} min ago`;
}

export function useNow(every = 30_000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), every);
    return () => clearInterval(t);
  }, [every]);
  return now;
}

export function BankStory() {
  const s = useBankDemo();
  const now = useNow();
  const storyT = s.transfers.find((t) => t.story);
  const done = [
    s.flaggedAt !== null,
    s.publicChecked,
    !!storyT,
    !!storyT,
    !!storyT && storyT.decision !== "pending",
    storyT?.decision === "confirmed",
  ];
  const steps: [string, string][] = [
    ["Extraction", "A persona gets the mule account from the scammer. Sockabus flags it."],
    ["Not public yet", "The mock public report check has no reports for it."],
    ["Victim tries to pay", "The customer sends money. DemoBank calls the Sockabus lookup."],
    ["Explained response", "Risk score, reason, first seen and the chat evidence. Not just yes or no."],
    ["Bank decides", "Held for review, not frozen. A person confirms or releases it."],
    ["Outcome", `RM prevented, flagged ${STORY_REPORT_DELAY_MIN} min before the first victim report.`],
  ];
  const current = done.indexOf(false);

  const fillVictim = () => {
    fillBank(STORY_BANK, STORY_ACCOUNT);
    setBankDraft({ amt: STORY_AMOUNT });
    document.getElementById("account-input")?.focus();
  };
  const reset = () => {
    resetBankDemo();
    clearLookup();
  };

  return (
    <section className="card bankstory" aria-label="Demo story" data-testid="bank-story">
      <div className="bs-h">
        <div>
          <span className="caps muted">The story · scripted mock data</span>
          <h2>Why a Sockabus flag comes first</h2>
        </div>
        <button type="button" className="btn btn-quiet btn-sm" onClick={reset} data-testid="story-reset">
          Reset story
        </button>
      </div>
      <ol className="bs-steps">
        {steps.map(([t, d], i) => (
          <li key={t} className={done[i] ? "done" : i === current ? "now" : ""} data-testid={`story-step-${i + 1}`} aria-current={i === current ? "step" : undefined}>
            <b className="n">{done[i] ? "✓" : i + 1}</b>
            <span>
              <b>{t}</b>
              <small>{d}</small>
            </span>
          </li>
        ))}
      </ol>
      <div className="bs-body">
        <div className="bs-chat" aria-label="Scripted scammer chat">
          <span className="caps muted">
            1 · {STORY_PERSONA} chatting with {STORY_SCAMMER}
          </span>
          {s.flaggedAt === null ? (
            <button type="button" className="btn btn-primary" onClick={flagStoryAccount} data-testid="story-play">
              Play the scammer chat
            </button>
          ) : (
            <>
              <div className="bs-msgs">
                {STORY_CHAT.map((m, i) => (
                  <p key={i} className={`bs-msg ${m.who}${i === STORY_EVIDENCE_LINE ? " hit" : ""}`}>
                    {m.text}
                  </p>
                ))}
              </div>
              <p className="bs-flag" data-testid="story-flagged">
                <b>Sockabus flagged {MASKED}</b> · {minutesAgo(s.flaggedAt, now)} · no one has paid yet
              </p>
            </>
          )}
        </div>
        <div className="bs-side">
          <div className="pubcheck" aria-label="Mock public report check">
            <span className="caps muted">2 · Mock public report check (illustrative)</span>
            {s.publicChecked ? (
              <p data-testid="public-check-result">
                <b>No reports found</b> for {MASKED}. Report-based lists learn about an account only after a victim pays and files a report.
              </p>
            ) : (
              <button type="button" className="btn btn-secondary btn-sm" onClick={markPublicChecked} disabled={s.flaggedAt === null} data-testid="story-public-check">
                Check {MASKED} on the public list
              </button>
            )}
          </div>
          <div className="pubcheck">
            <span className="caps muted">3 · The victim pays</span>
            <button type="button" className="btn btn-secondary btn-sm" onClick={fillVictim} data-testid="story-fill">
              Fill the victim&apos;s transfer (RM {STORY_AMOUNT})
            </button>
            <small className="muted">Then press Transfer. Steps 4 to 6 happen in the transfer result and the fraud console.</small>
          </div>
        </div>
      </div>
    </section>
  );
}
