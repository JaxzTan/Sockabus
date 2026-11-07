// Stand-ins for backend work, so the demo runs without the Go API.
// Real system: classification, extraction and persona replies run on the backend only (spec Area 6 swap list).
import { digits } from "./format";
import type { Conversation, Entity, EntityType, Lookup, ScamType } from "./types";
import { assess } from "./risk";

export function classify(text: string): ScamType | null {
  const t = text.toLowerCase();
  if (/parcel|poslaju|customs|redeliver|j&t/.test(t)) return "parcel";
  if (/invest|return|usdt|crypto|profit/.test(t)) return "investment";
  if (/loan|pinjaman|approved/.test(t)) return "loan";
  if (/part-time|reviewer|job|task|commission|click like/.test(t)) return "job";
  if (/police|polis|bank negara|lhdn|court|mahkamah/.test(t)) return "impersonation";
  if (/love|dear|sayang|darling/.test(t)) return "romance";
  return null;
}

export interface Found {
  type: EntityType;
  bank: string;
  value: string;
}

export function extract(text: string): Found[] {
  const out: Found[] = [];
  const low = text.toLowerCase();
  const acct = text.match(/\b(\d[\d\s-]{8,18}\d)\b/);
  if (acct && !/^\+?60/.test(acct[1].trim())) {
    const d = digits(acct[1]);
    if (d.length >= 10 && d.length <= 16) {
      const bank = /cimb/.test(low) ? "CIMB" : /public bank|pbb/.test(low) ? "PBB" : /rhb/.test(low) ? "RHB" : "MBB";
      out.push({ type: "bank_account", bank, value: d });
    }
  }
  const w = text.match(/\bT[1-9A-HJ-NP-Za-km-z]{33}\b/);
  if (w) out.push({ type: "wallet", bank: "TRON", value: w[0] });
  const ph = text.match(/\+?60\s?1\d[\s-]?\d{3,4}[\s-]?\d{4}/);
  if (ph) out.push({ type: "phone", bank: "", value: ph[0] });
  const u = text.match(/\b[a-z0-9-]+(\.[a-z0-9-]+)+\.(top|xyz|com|my|net|info)(\/\S*)?/i);
  if (u && !/@/.test(u[0])) out.push({ type: "url", bank: "", value: u[0] });
  return out;
}

// Returns [reply, new stage].
export function personaReply(c: Conversation, text: string, hasEntity: boolean): [string, number] {
  const t = text.toLowerCase();
  if (hasEntity) return ["ok wait ah, my nephew help me do the transfer tonight. the name must write exactly like that?", 3];
  if (/bot|robot|\bai\b|chatgpt/.test(t)) return ["haiya what bot, i auntie la. my grandson also say i type very slow", c.stage];
  if (/deposit|pay|transfer|fee|bank in|activate/.test(t)) return ["ok la i try.. which bank ah? i use maybank app", Math.max(c.stage, 2)];
  if (/rm\s?\d|job|part-time|return|profit|loan/.test(t)) return ["wah really ah? i retired already, can do from home or not?", Math.max(c.stage, 1)];
  if (/hurry|fast|cepat|tonight|now/.test(t)) return ["aiyo dont rush me la, i old already slow slow one", c.stage];
  return ["sorry ah i not so understand, can explain again?", c.stage];
}

// Mirrors POST /v1/lookup. Two results only (spec D1): match → held, no match → sent.
// Like the Go API, rejected entities never match, and the confidence is whatever the entity has.
export function lookup(entities: Entity[], bank: string, acct: string): Omit<Lookup, "bank" | "amt"> {
  const d = digits(acct);
  const e = entities.find((x) => x.type === "bank_account" && x.bank === bank && x.value === d);
  if (!e || e.status === "rejected") return { result: "sent", entityId: null, res: { match: false }, ms: 18 };
  const res = { match: true, confidence: e.conf, status: e.status, scam_types: [e.scam], first_seen: e.first, sightings: e.sightings, ...assess("bank_account", e.status, e.conf, e.sightings) };
  return { result: "held", entityId: e.id, res, ms: 41 };
}
