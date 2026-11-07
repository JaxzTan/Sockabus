// The scripted bank-simulator story (demo spec: extraction → not public yet → victim pays →
// explained response → bank decides → outcome). Everything here is mock data: a fake bank
// ("DemoBank", code DEMO, which extraction never produces, so it can never match a real record),
// a fake account number and a scripted chat. No real account numbers appear in the story.
import { assess } from "./risk";
import type { LookupResponse } from "./types";

export const STORY_BANK = "DEMO";
export const STORY_BANK_NAME = "DemoBank";
export const STORY_ACCOUNT = "000012345678"; // fake; shown grouped as 0000 1234 5678
export const STORY_AMOUNT = "2500.00";
export const STORY_PERSONA = "Auntie Mei";
export const STORY_SCAMMER = "@gold_mentor_lim";
// Scripted: when the first victim report would reach the public, report-based lists (minutes after
// Sockabus flagged the account). Report-based lists only learn of an account after someone has paid.
export const STORY_REPORT_DELAY_MIN = 47;
export const STORY_SIGHTINGS = 3;

export const STORY_CHAT: { who: "scammer" | "persona"; text: string }[] = [
  { who: "scammer", text: "Hi aunty, our gold plan pays 8% every week. Very safe one, Bank Negara approved" },
  { who: "persona", text: "wah 8% ah.. how to join? my son say must be careful" },
  { who: "scammer", text: "Easy aunty. First deposit RM2,500 only, can withdraw anytime" },
  { who: "scammer", text: "ok sis bank in to this acc first, DemoBank 0000 1234 5678" },
  { who: "persona", text: "ok wait ah i ask Kelvin help me transfer" },
];

// The scammer message the account was extracted from (index into STORY_CHAT).
export const STORY_EVIDENCE_LINE = 3;

// The response the story's lookup returns: built with the same rules as the live API
// (internal/risk via ./risk) for an operator-verified account seen 3 times.
export function storyResponse(flaggedAt: number): LookupResponse {
  const first = new Date(flaggedAt).toISOString();
  return {
    match: true,
    confidence: "high",
    status: "verified",
    scam_types: ["investment"],
    first_seen: first,
    last_seen: first,
    sightings: STORY_SIGHTINGS,
    ...assess("bank_account", "verified", "high", STORY_SIGHTINGS),
    evidence: {
      excerpt: "ok sis bank in to this acc first, DemoBank 0000xxxx",
      transcript_ref: "conv_demo",
      persona_id: "p_demo",
      captured_at: first,
    },
  };
}

// Two more fake DemoBank accounts with a fixed outcome, for the customer view's quick fill: one
// flagged and still under review (warning), one the bank already finished reviewing (blocked).
export const DEMO_WARN_ACCOUNT = "000011112222";
export const DEMO_BLOCKED_ACCOUNT = "000033334444";

// Scripted outcome for those two accounts, or null for any other account.
export function fixedDemoOutcome(bank: string, acctDigits: string): { kind: "warn" | "blocked"; res: LookupResponse } | null {
  if (bank !== STORY_BANK) return null;
  const kind = acctDigits === DEMO_WARN_ACCOUNT ? "warn" : acctDigits === DEMO_BLOCKED_ACCOUNT ? "blocked" : null;
  if (!kind) return null;
  // Warning: flagged 12 minutes ago, unverified. Blocked: flagged 2 days ago, verified and reviewed.
  const ago = kind === "warn" ? 12 * 60_000 : 2 * 24 * 3600_000;
  const first = new Date(Date.now() - ago).toISOString();
  const verified = kind === "blocked";
  const sightings = verified ? 4 : 1;
  return {
    kind,
    res: {
      match: true,
      confidence: verified ? "high" : "medium",
      status: verified ? "verified" : "unverified",
      scam_types: [verified ? "job" : "investment"],
      first_seen: first,
      last_seen: first,
      sightings,
      ...assess("bank_account", verified ? "verified" : "unverified", verified ? "high" : "medium", sightings),
    },
  };
}

export const isStoryAccount = (bank: string, acctDigits: string) => bank === STORY_BANK && acctDigits === STORY_ACCOUNT;
