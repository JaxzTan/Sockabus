// The bank simulator's mock fraud console: the scripted story's progress, transfers the simulator
// held, and the reviewer's decisions. Browser-only mock state (localStorage, per viewer): nothing here
// is sent to Sockabus, because a real bank keeps its own review queue.
import { useSyncExternalStore } from "react";
import type { RecommendedAction } from "./types";

export type HoldDecision = "pending" | "confirmed" | "released";
export type MuleReview = "none" | "freeze_requested" | "cleared";

export interface FlaggedTransfer {
  id: string;
  at: number;
  bank: string; // recipient bank code
  acctMasked: string; // never the full number
  amt: number;
  score: number;
  action: RecommendedAction;
  story: boolean; // the scripted story's transfer (has the mock victim-report timing)
  sameBank: boolean; // recipient is at DemoBank too, so DemoBank can review the mule account
  decision: HoldDecision;
  mule: MuleReview;
}

export interface BankDemo {
  flaggedAt: number | null; // story step 1: when Sockabus flagged the scripted account
  publicChecked: boolean; // story step 2
  transfers: FlaggedTransfer[]; // newest first
}

const KEY = "sockabus-bank-demo-v1";
const EMPTY: BankDemo = { flaggedAt: null, publicChecked: false, transfers: [] };
const MAX_TRANSFERS = 20;

let state: BankDemo = EMPTY;
let loaded = false;
const subs = new Set<() => void>();

function load() {
  if (loaded || typeof window === "undefined") return;
  loaded = true;
  try {
    const raw = window.localStorage.getItem(KEY);
    if (raw) state = { ...EMPTY, ...(JSON.parse(raw) as Partial<BankDemo>) };
  } catch {
    // storage blocked or corrupt: start empty
  }
}

function update(fn: (s: BankDemo) => BankDemo) {
  load();
  state = fn(state);
  try {
    window.localStorage.setItem(KEY, JSON.stringify(state));
  } catch {
    // private window: keep it in memory only
  }
  subs.forEach((f) => f());
}

const subscribe = (f: () => void) => {
  subs.add(f);
  return () => subs.delete(f);
};
const snapshot = () => {
  load();
  return state;
};

export const getBankDemo = snapshot;
export const useBankDemo = () => useSyncExternalStore(subscribe, snapshot, () => EMPTY);

export const flagStoryAccount = () => update((s) => ({ ...s, flaggedAt: s.flaggedAt ?? Date.now() }));
export const markPublicChecked = () => update((s) => ({ ...s, publicChecked: true }));
export const resetBankDemo = () => update(() => EMPTY);

// Every held transfer lands in the review queue (recommend, don't auto-freeze: a person decides).
export function recordHeld(t: Omit<FlaggedTransfer, "id" | "at" | "decision" | "mule">) {
  const at = Date.now();
  update((s) => ({ ...s, transfers: [{ ...t, id: `t_${at}`, at, decision: "pending" as const, mule: "none" as const }, ...s.transfers].slice(0, MAX_TRANSFERS) }));
}

export const decideHold = (id: string, decision: HoldDecision) => update((s) => ({ ...s, transfers: s.transfers.map((t) => (t.id === id ? { ...t, decision } : t)) }));
export const reviewMule = (id: string, mule: MuleReview) => update((s) => ({ ...s, transfers: s.transfers.map((t) => (t.id === id ? { ...t, mule } : t)) }));

// Money kept from scammers: holds a reviewer confirmed.
export const prevented = (s: BankDemo) => s.transfers.filter((t) => t.decision === "confirmed").reduce((n, t) => n + t.amt, 0);
