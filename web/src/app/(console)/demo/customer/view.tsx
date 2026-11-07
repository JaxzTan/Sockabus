"use client";
// What a customer at another bank sees when they type a flagged account in by hand. Same lookup as
// the bank simulator; the screen depends on how far the receiving bank's review has gone:
//   blocked: DemoBank's reviewer finished (hold confirmed or freeze requested, the story's last stage)
//   warning: Sockabus flagged it but the review is not finished: warn, let the customer decide
//   sent:    no record
import { useState } from "react";
import { Badge, Head, Spin } from "@/components/ui";
import { api, stamp, type ApiLookupResult } from "@/lib/api";
import { acctKey, isBlocked, useBankDemo } from "@/lib/bankDemo";
import {
  DEMO_BLOCKED_ACCOUNT,
  DEMO_CLEAN_ACCOUNT,
  DEMO_WARN_ACCOUNT,
  fixedDemoOutcome,
  isStoryAccount,
  STORY_ACCOUNT,
  STORY_AMOUNT,
  STORY_BANK,
  STORY_BANK_NAME,
  storyResponse,
} from "@/lib/bankStory";
import { BANKS, digits, groupAcct, maskDigits } from "@/lib/format";
import { lookup as sampleLookup } from "@/lib/sim";
import { USE_SAMPLE, useStore } from "@/lib/store";
import type { LookupResponse } from "@/lib/types";

type Outcome = { kind: "blocked" | "warn" | "sent"; res: LookupResponse; bank: string; acct: string; amt: string; at: number; proceeded?: boolean };

const bankName = (code: string) => (code === STORY_BANK ? STORY_BANK_NAME : (BANKS[code] ?? code));

export function CustomerView() {
  const demo = useBankDemo();
  const { data } = useStore();
  const [bank, setBank] = useState(STORY_BANK);
  const [acct, setAcct] = useState("");
  const [amt, setAmt] = useState("500.00");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [out, setOut] = useState<Outcome | null>(null);

  const check = async () => {
    const d = digits(acct);
    if (d.length < 10) {
      setError("Enter the full account number (at least 10 digits).");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const fixed = fixedDemoOutcome(bank, d);
      if (fixed) {
        setOut({ ...fixed, bank, acct: d, amt, at: Date.now() });
        return;
      }
      let res: LookupResponse;
      if (isStoryAccount(bank, d)) {
        // The story's fake account is only "known" once Sockabus flagged it (story step 1).
        res = demo.flaggedAt === null ? { match: false } : storyResponse(demo.flaggedAt);
      } else if (USE_SAMPLE) {
        res = sampleLookup(data.entities, bank, d).res;
      } else {
        res = (await api<ApiLookupResult>("/demo/lookup", { body: { bank, account: d } })).res;
      }
      const kind = !res.match ? "sent" : isBlocked(demo, acctKey(bank, d)) ? "blocked" : "warn";
      setOut({ kind, res, bank, acct: d, amt, at: Date.now() });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const fill = (a: string, amount: string) => {
    setBank(STORY_BANK);
    setAcct(groupAcct(a));
    setAmt(amount);
    setError(null);
  };

  const again = () => {
    setOut(null);
    setAcct("");
  };

  return (
    <>
      <Head
        title="Customer view"
        sub="What a customer at another bank sees when they type a flagged account in by hand. Blocked once DemoBank's review is done; a warning before that."
        right={<Badge word="Demo only · not a real bank" tone="neutral" />}
      />
      <div className="cust-wrap">
        <section className="card cust" aria-label="OtherBank mobile transfer" data-testid="customer-app">
          <div className="cust-h">
            <span className="sq">OB</span>
            <b>OtherBank · Transfer</b>
          </div>
          {!out ? (
            <form
              noValidate
              className="cust-form"
              onSubmit={(ev) => {
                ev.preventDefault();
                void check();
              }}
            >
              <div className="field">
                <label htmlFor="cust-bank">To bank</label>
                <select className="input" id="cust-bank" value={bank} onChange={(ev) => setBank(ev.target.value)} data-testid="cust-bank">
                  <option value={STORY_BANK}>{STORY_BANK_NAME} (demo)</option>
                  {Object.entries(BANKS).map(([k, v]) => (
                    <option key={k} value={k}>
                      {v}
                    </option>
                  ))}
                </select>
              </div>
              <div className="field">
                <label htmlFor="cust-acct">Account number</label>
                <input className="input mono" id="cust-acct" inputMode="numeric" autoComplete="off" placeholder="e.g. 5621 0384 11" value={acct} onChange={(ev) => setAcct(ev.target.value)} data-testid="cust-acct" />
              </div>
              <div className="field">
                <label htmlFor="cust-amt">Amount (RM)</label>
                <input className="input mono" id="cust-amt" inputMode="decimal" value={amt} onChange={(ev) => setAmt(ev.target.value)} data-testid="cust-amt" />
              </div>
              <div className="quick">
                <span>Quick fill:</span>
                <button
                  type="button"
                  onClick={() => {
                    setBank(STORY_BANK);
                    setAcct(groupAcct(STORY_ACCOUNT));
                    setAmt(STORY_AMOUNT);
                  }}
                  data-testid="cust-fill-story"
                >
                  Story mule account
                </button>
                <button type="button" onClick={() => fill(DEMO_WARN_ACCOUNT, "800.00")} data-testid="cust-fill-warn">
                  Demo: warning
                </button>
                <button type="button" onClick={() => fill(DEMO_BLOCKED_ACCOUNT, "1200.00")} data-testid="cust-fill-blocked">
                  Demo: blocked
                </button>
                <button type="button" onClick={() => fill(DEMO_CLEAN_ACCOUNT, "350.00")} data-testid="cust-fill-success">
                  Demo: success
                </button>
              </div>
              {error && (
                <p className="cust-err" role="alert">
                  {error}
                </p>
              )}
              <button className="btn btn-primary" disabled={busy} data-testid="cust-transfer">
                {busy ? (
                  <>
                    <Spin /> Checking…
                  </>
                ) : (
                  `Transfer RM ${amt || "0.00"}`
                )}
              </button>
            </form>
          ) : (
            <Result out={out} onBack={again} onProceed={() => setOut({ ...out, proceeded: true, at: Date.now() })} />
          )}
        </section>
        <aside className="cust-note">
          <span className="caps muted">How this screen is chosen</span>
          <ul>
            <li>
              <b>Blocked</b>: the receiving bank finished its review (in the bank simulator&apos;s fraud console, <i>Confirm hold</i> or <i>Request freeze</i>).
            </li>
            <li>
              <b>Warning</b>: Sockabus flagged the account, but the review is not finished. The customer is warned and decides.
            </li>
            <li>
              <b>Sent</b>: no scam record for the account.
            </li>
          </ul>
          <p className="muted">
            Quick fill: <i>Demo: warning</i>, <i>Demo: blocked</i> and <i>Demo: success</i> always show that screen (scripted fake accounts). <i>Story mule account</i> follows the bank simulator story: flagged after step 1, blocked after
            the review. Review outcomes are kept in this browser only.
          </p>
        </aside>
      </div>
    </>
  );
}

function Result({ out, onBack, onProceed }: { out: Outcome; onBack: () => void; onProceed: () => void }) {
  const to = `${bankName(out.bank)} ${maskDigits(out.acct)}`;
  const scam = out.res.scam_types?.[0] ?? "scam";
  const aScam = `${/^[aeiou]/i.test(scam) ? "an" : "a"} ${scam}`;
  if (out.kind === "blocked") {
    return (
      <div className="cust-res blocked" role="alert" data-testid="cust-blocked">
        <span className="ic">⛔</span>
        <h2>Account blocked</h2>
        <p>
          You can&apos;t transfer to <b className="mono">{to}</b>. It was reported for {aScam} scam and blocked after the bank&apos;s review.
        </p>
        <p className="muted">Your RM {out.amt} has not left your account. If someone asked you to pay this account, stop and call your bank.</p>
        <button className="btn btn-primary" onClick={onBack} data-testid="cust-back">
          Back
        </button>
      </div>
    );
  }
  if (out.kind === "warn" && !out.proceeded) {
    return (
      <div className="cust-res warn" role="alert" data-testid="cust-warn">
        <span className="ic">!</span>
        <h2>Warning: possible scam</h2>
        <p>
          <b className="mono">{to}</b> may be linked to {aScam} scam. It was flagged {out.res.first_seen ? stamp(out.res.first_seen) : "recently"} and is under review.
        </p>
        <p className="muted">Scammers often promise high returns or ask for a deposit first. Only continue if you know and trust this person.</p>
        <div className="row-gap">
          <button className="btn btn-primary" onClick={onBack} data-testid="cust-cancel">
            Cancel transfer
          </button>
          <button className="btn btn-quiet" onClick={onProceed} data-testid="cust-proceed">
            I understand the risk, continue
          </button>
        </div>
      </div>
    );
  }
  return (
    <div className="cust-res sent" role="status" data-testid="cust-sent">
      <span className="ic">✓</span>
      <h2>Transfer successful</h2>
      <p>
        RM {out.amt} sent to <b className="mono">{to}</b>.
      </p>
      <dl className="cust-receipt" data-testid="cust-receipt">
        <dt>Reference</dt>
        <dd className="mono">OB{String(out.at).slice(-8)}</dd>
        <dt>Date</dt>
        <dd>{stamp(new Date(out.at).toISOString())}</dd>
        <dt>Status</dt>
        <dd>Completed</dd>
      </dl>
      {out.proceeded && <p className="muted">You continued after a scam warning.</p>}
      <button className="btn btn-secondary" onClick={onBack} data-testid="cust-new">
        New transfer
      </button>
    </div>
  );
}
