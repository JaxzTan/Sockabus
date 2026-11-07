// Shapes the console works with. Field names follow the prototype; map from the Go API when it is wired in.

export type PersonaStatus = "draft" | "active" | "paused" | "retired";
export type ConvoStatus = "bot" | "operator" | "closed";
export type EntityStatus = "unverified" | "verified" | "rejected";
export type Confidence = "low" | "medium" | "high";
export type EntityType = "bank_account" | "wallet" | "phone" | "url" | "handle";
export type ScamType = "parcel" | "job" | "investment" | "impersonation" | "romance" | "loan" | "other";
export type AvatarTone = "mustard" | "blue" | "green" | "neutral";
export type Sender = "scammer" | "persona" | "operator";

export interface Persona {
  id: string;
  name: string;
  age: number;
  loc: string;
  occ: string;
  langs: string[];
  status: PersonaStatus;
  handle: string;
  created: string;
  backstory: string;
  wealth: string;
  av: AvatarTone;
  gen?: boolean; // made with "Generate persona" in this session (drives the demo path)
  detail?: PersonaDetail; // API mode: everything GET /internal/personas returns, for the Modify panel
}

// Raw persona fields from the API. profile and wealth_profile are free-form JSON written by the
// persona generator (internal/persona/generator_spec.md), so every key is optional.
export interface PersonaDetail {
  profile: Record<string, unknown> | null;
  wealth: Record<string, unknown> | null;
  langMix: [code: string, weight: number][] | null; // weights, when the API sends them
  promptVersion: number | null;
  scamTypes: string[];
  updated: string | null;
}

export interface Message {
  who: Sender;
  text: string;
  t: string;
  at?: number; // ms when sent, when known (API messages and ones sent in this browser)
  undecryptable?: boolean; // stored ciphertext could not be decrypted; shown as a muted placeholder
}

export interface Conversation {
  id: string;
  scammer: string;
  personaId: string;
  scam: ScamType;
  status: ConvoStatus;
  stage: number; // index into STAGES
  last: string;
  lastAt?: number; // ms of the last message (API mode only), for the "stuck over 2 h" check
  msgs: Message[];
  web?: boolean; // started from the web chat page instead of Telegram
}

export interface Entity {
  id: string;
  type: EntityType;
  bank: string; // bank code for accounts, chain for wallets, "" otherwise
  value: string;
  scam: ScamType;
  status: EntityStatus;
  conf: Confidence;
  sightings: number;
  first: string;
  firstAt?: number; // ms of first sighting, for "waiting longest" ordering
  convoId: string | null;
  stageAt?: number;
  sample: boolean; // seeded row, always labelled SAMPLE
  note?: string;
}

export type LookupResult = "held" | "sent";

export type RecommendedAction = "HOLD_FOR_REVIEW" | "WARN_CUSTOMER" | "MONITOR";

// POST /v1/lookup. A miss is exactly { match: false }; a hit carries the rule-based risk fields
// (internal/risk, web/src/lib/risk.ts) and, for the operator token only, masked chat evidence.
export interface LookupResponse {
  match: boolean;
  confidence?: Confidence;
  status?: string;
  scam_types?: ScamType[];
  first_seen?: string;
  last_seen?: string;
  sightings?: number;
  risk_score?: number;
  reason?: string;
  signals?: string[];
  recommended_action?: RecommendedAction;
  evidence?: { excerpt: string; transcript_ref: string; persona_id?: string; captured_at: string };
}

export interface Lookup {
  result: LookupResult;
  entityId: string | null;
  res: LookupResponse;
  bank: string;
  acct?: string; // digits only, kept in this browser (never in the URL)
  amt: string;
  ms: number;
  scripted?: boolean; // the bank story's lookup, answered in the browser from scripted data
}

export interface BankDraft {
  bank: string;
  acct: string;
  amt: string;
}
