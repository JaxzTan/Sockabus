import type { Entity } from "./types";

export const STAGES = ["Rapport", "Bait", "Payment request", "Stall"];
// Every bank code the backend extractor saves (internal/extraction), as text only: no logos (spec §4.3).
export const BANKS: Record<string, string> = {
  MBB: "Maybank", CIMB: "CIMB", PBB: "Public Bank", RHB: "RHB", HLB: "Hong Leong",
  BIMB: "Bank Islam", AMB: "AmBank", BSN: "BSN", UOB: "UOB", OCBC: "OCBC",
};

export const digits = (s: string | undefined) => (s ?? "").replace(/\D/g, "");
// Same rule as internal/extraction.MaskDigits: a run of 6+ digits (spaces, dashes or dots allowed
// between them) keeps its first 4 digits, the rest becomes "xxxx". Used for anything shown in the demo.
export const maskDigits = (s: string) => s.replace(/\d(?:[ .\-]?\d){5,}/g, (run) => run.replace(/\D/g, "").slice(0, 4) + "xxxx");
export const groupAcct = (d: string) => d.replace(/^(\d{4})(\d{4})(\d+)$/, "$1 $2 $3");
export const clock = () => new Date().toTimeString().slice(0, 5);
export const today = () => new Date().toLocaleDateString("en-GB", { day: "numeric", month: "short" });
export const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

const TITLES = /^(uncle|aunty|auntie|kak|encik|puan)$/i;
export const initials = (name: string) =>
  name
    .split(/\s+/)
    .filter((w) => !TITLES.test(w))
    .map((w) => w[0])
    .join("")
    .slice(0, 2)
    .toUpperCase() || name.slice(0, 2).toUpperCase();

export const typeLabel = (e: Pick<Entity, "type" | "bank">) =>
  ({ bank_account: `Bank · ${e.bank}`, wallet: `Wallet · ${e.bank}`, phone: "Phone", url: "URL", handle: "Handle" })[e.type];

// Short form for lists: grouped account number, shortened wallet.
export const showVal = (e: Pick<Entity, "type" | "value">) =>
  e.type === "bank_account" ? groupAcct(e.value) : e.type === "wallet" ? `${e.value.slice(0, 6)}…${e.value.slice(-7)}` : e.value;

// Full form for the evidence panel.
export const fullVal = (e: Pick<Entity, "type" | "value">) => (e.type === "bank_account" ? groupAcct(e.value) : e.value);
