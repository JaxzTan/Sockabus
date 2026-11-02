// Browser-side calls to the Go API through the /api proxy, plus mappers from the API's JSON
// to the shapes the screens use (types.ts). Field names follow the Go handlers in
// internal/server/live.go (there is no api/openapi.yaml yet).
import { today } from "./format";
import type { AvatarTone, Confidence, Conversation, Entity, EntityType, LookupResponse, Message, Persona, PersonaDetail, PersonaStatus, ScamType } from "./types";

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

// The Go API answers errors as { "error": "text" }. The spec's { "error": { "code", "message" } } is read too.
function errorText(j: unknown): string | undefined {
  const e = (j as { error?: unknown } | null)?.error;
  if (typeof e === "string") return e;
  if (e && typeof (e as { message?: unknown }).message === "string") return (e as { message: string }).message;
  return undefined;
}

export async function api<T>(path: string, opts: { method?: string; body?: unknown; keepalive?: boolean; signal?: AbortSignal } = {}): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`/api${path}`, {
      method: opts.method ?? (opts.body === undefined ? "GET" : "POST"),
      headers: opts.body === undefined ? undefined : { "content-type": "application/json" },
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      keepalive: opts.keepalive,
      signal: opts.signal,
      cache: "no-store",
    });
  } catch {
    throw new ApiError("Can't reach the Sockabus console server. Check your connection.", 0);
  }
  const text = await res.text();
  let json: unknown = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    // non-JSON body (e.g. a proxy error page)
  }
  // Session expired or signed out in another tab: the proxy answers 401, so go to the sign-in page.
  if (res.status === 401 && typeof window !== "undefined" && location.pathname !== "/sign-in") {
    // Full page load on purpose: drops all in-memory data and lets the proxy check the cookie again.
    // eslint-disable-next-line @next/next/no-location-assign-relative-destination
    location.assign(`/sign-in?next=${encodeURIComponent(location.pathname + location.search)}`);
  }
  if (!res.ok) throw new ApiError(errorText(json) ?? `Request failed (${res.status})`, res.status);
  return json as T;
}

// ---------- API shapes (Go JSON) ----------

export interface ApiPersona {
  id: number;
  name: string;
  age: number | null;
  language_mix: Record<string, number> | string[] | null;
  backstory: string | null;
  wealth_profile: ({ savings_rm?: number; income_bracket?: string; crypto_familiarity?: string } & Record<string, unknown>) | null;
  status: PersonaStatus;
  created_at: string;
  updated_at?: string;
  profile: ({ city?: string; state?: string; occupation?: string } & Record<string, unknown>) | null;
  prompt_version: number;
  scam_types: string[];
}

export interface ApiAccount {
  id: number;
  persona_id: number;
  channel: "telegram" | "whatsapp";
  phone: string;
  session_ref: string;
  status: "active" | "banned" | "cooldown";
}

// sqlc NullScamType: the Go API sends an object, not a string.
type NullScamType = { scam_type: ScamType; valid: boolean } | null;

export interface ApiConversation {
  id: number;
  account_id: number;
  scammer_handle: string;
  scam_type: NullScamType;
  stage: string;
  status: "bot" | "human" | "closed";
  started_at: string;
  last_message_at: string | null;
  persona_id: number;
}

export interface ApiMessage {
  id: number;
  direction: "inbound" | "outbound";
  sender_type: "scammer" | "persona" | "operator";
  text: string;
  sent_at: string;
  undecryptable?: boolean; // the API could not decrypt it; text is a placeholder
}

export interface ApiEntity {
  id: number;
  type: EntityType;
  value_normalized: string;
  bank_or_chain: string | null;
  confidence: Confidence;
  status: Entity["status"];
  sightings_count: number;
  first_seen: string;
  last_seen: string;
}

export interface ApiConversationDetail {
  conversation: ApiConversation;
  messages: ApiMessage[];
  entities: ApiEntity[];
}

export interface ApiGenerateResult {
  personas: { id: number; name: string; status: string }[];
  notes: string[];
}

export interface ApiLookupResult {
  res: LookupResponse;
  ms: number;
}

// ---------- mappers ----------

const SIM_PREFIX = "sim:"; // internal/channel/sim SessionPrefix: web chat accounts
export const isSimAccount = (a: ApiAccount) => a.session_ref.startsWith(SIM_PREFIX);

const LANGS: Record<string, string> = { en: "EN", ms: "BM", zh: "Mandarin", nan: "Hokkien", yue: "Cantonese", hak: "Hakka", ta: "Tamil", manglish: "Manglish" };
export const langName = (code: string) => LANGS[code.toLowerCase()] ?? code;
const TONES: AvatarTone[] = ["mustard", "blue", "green", "neutral"];

// Only ISO timestamps from the API are parsed; anything else (sample data) is shown as is.
const parse = (iso: string) => (/^\d{4}-\d{2}-\d{2}T/.test(iso) ? new Date(iso) : null);

// "08:12" for today, "28 Oct" before.
export function when(iso: string | null | undefined): string {
  if (!iso) return "—";
  const d = parse(iso);
  if (!d) return iso;
  const day = d.toLocaleDateString("en-GB", { day: "numeric", month: "short" });
  return day === today() ? d.toTimeString().slice(0, 5) : day;
}

// "29 Oct, 08:12"
export function stamp(iso: string | null | undefined): string {
  if (!iso) return "—";
  const d = parse(iso);
  if (!d) return iso;
  return `${d.toLocaleDateString("en-GB", { day: "numeric", month: "short" })}, ${d.toTimeString().slice(0, 5)}`;
}

function wealthText(w: ApiPersona["wealth_profile"]): string {
  if (!w) return "";
  const parts = [];
  if (w.savings_rm != null) parts.push(`RM${w.savings_rm.toLocaleString("en-MY")} savings`);
  if (w.income_bracket) parts.push(w.income_bracket);
  return parts.join(" · ");
}

function langList(mix: ApiPersona["language_mix"]): string[] {
  if (!mix) return [];
  const keys = Array.isArray(mix) ? mix : Object.entries(mix).sort((a, b) => b[1] - a[1]).map(([k]) => k);
  return keys.map((k) => LANGS[k.toLowerCase()] ?? k);
}

export function toPersona(p: ApiPersona, accounts: ApiAccount[], genIds: string[]): Persona {
  const id = String(p.id);
  const mine = accounts.filter((a) => a.persona_id === p.id);
  const real = mine.find((a) => !isSimAccount(a));
  return {
    id,
    name: p.name,
    age: p.age ?? 0,
    loc: p.profile?.city ?? p.profile?.state ?? "—",
    occ: p.profile?.occupation ?? "",
    langs: langList(p.language_mix),
    status: p.status,
    handle: real ? real.phone : mine.length ? "web chat only" : "not bound",
    created: when(p.created_at),
    backstory: p.backstory ?? "",
    wealth: wealthText(p.wealth_profile),
    av: TONES[p.id % TONES.length],
    gen: genIds.includes(id),
    detail: personaDetail(p),
  };
}

const isObj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v);

function personaDetail(p: ApiPersona): PersonaDetail {
  const mix = p.language_mix;
  const weights = isObj(mix)
    ? Object.entries(mix)
        .filter((e): e is [string, number] => typeof e[1] === "number" && e[1] > 0)
        .sort((a, b) => b[1] - a[1])
    : null;
  return {
    profile: isObj(p.profile) ? p.profile : null,
    wealth: isObj(p.wealth_profile) ? p.wealth_profile : null,
    langMix: weights && weights.length ? weights : null,
    promptVersion: p.prompt_version > 0 ? p.prompt_version : null,
    scamTypes: p.scam_types ?? [],
    updated: p.updated_at ?? null,
  };
}

const STAGE_KEYS = ["rapport", "bait", "payment_request", "stall"];

export function toMessage(m: ApiMessage): Message {
  return { who: m.sender_type, text: m.text, t: when(m.sent_at), at: Date.parse(m.sent_at) || undefined, undecryptable: m.undecryptable || undefined };
}

export function toConversation(c: ApiConversation, msgs: ApiMessage[] | undefined, accounts: ApiAccount[]): Conversation {
  const acct = accounts.find((a) => a.id === c.account_id);
  return {
    id: String(c.id),
    scammer: c.scammer_handle,
    personaId: String(c.persona_id),
    scam: c.scam_type?.valid ? c.scam_type.scam_type : "other",
    status: c.status === "human" ? "operator" : c.status,
    stage: Math.max(0, STAGE_KEYS.indexOf(c.stage)),
    last: when(c.last_message_at ?? c.started_at),
    lastAt: Date.parse(c.last_message_at ?? c.started_at) || undefined,
    msgs: (msgs ?? []).map(toMessage),
    web: acct ? isSimAccount(acct) : false,
  };
}

// The entity list has no scam type or source chat; both come from the conversation that captured it.
export function toEntity(e: ApiEntity, source?: { convoId: string; scam: ScamType }): Entity {
  const chain = e.bank_or_chain ?? "";
  return {
    id: String(e.id),
    type: e.type,
    bank: e.type === "wallet" ? chain.toUpperCase() : chain,
    value: e.value_normalized,
    scam: source?.scam ?? "other",
    status: e.status,
    conf: e.confidence,
    sightings: e.sightings_count,
    first: stamp(e.first_seen),
    firstAt: Date.parse(e.first_seen) || undefined,
    convoId: source?.convoId ?? null,
    sample: false,
  };
}
