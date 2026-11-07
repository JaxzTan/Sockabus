"use client";
// Console data store. Two sources behind one shape (Data), so the screens don't care which:
//   NEXT_PUBLIC_USE_SAMPLE_DATA=true → sample data in the browser (localStorage), sim.ts stand-ins.
//   otherwise                        → the Go API through the /api proxy, polled every 3 s.
// Real calls (spec Area 6.3; paths and fields from internal/server/live.go):
//   generatePersona  → POST /internal/personas/generate     setPersonaStatus → POST /internal/personas/{id}/status
//   scammerSends     → POST /internal/sim/accounts + /internal/sim/messages (dev chat simulator)
//   takeover/handback → POST /internal/conversations/{id}/takeover   operatorSend → POST …/operator-message
//   decide           → POST /internal/entities/{id}/verify after the Undo window (§4.5)
//   runLookup        → POST /api/demo/lookup (Next server route) → POST /v1/lookup; token stays server-side
//   reads            → GET /internal/personas, /accounts, /conversations, /conversations/{id}, /entities, /readyz
// View state (selection, filters) is in the URL, not here (spec §4.2).
import { useSyncExternalStore } from "react";
import {
  api,
  ApiError,
  toConversation,
  toEntity,
  toPersona,
  type ApiAccount,
  type ApiConversation,
  type ApiConversationDetail,
  type ApiEntity,
  type ApiGenerateResult,
  type ApiLookupResult,
  type ApiPersona,
} from "./api";
import { clock, digits, groupAcct, maskDigits, showVal, today, typeLabel } from "./format";
import { flagStoryAccount, getBankDemo, recordHeld, resetBankDemo } from "./bankDemo";
import { isStoryAccount, STORY_BANK, storyResponse } from "./bankStory";
import { GENERATED, SEED_CONVOS, SEED_ENTITIES, SEED_PERSONAS } from "./seed";
import { classify, extract, lookup, personaReply } from "./sim";
import type { BankDraft, Conversation, Entity, Lookup, PersonaStatus } from "./types";

export const USE_SAMPLE = process.env.NEXT_PUBLIC_USE_SAMPLE_DATA === "true";

// Sample mode saves all data; API mode saves only per-browser conveniences (bank form, script ticks...).
const KEY = USE_SAMPLE ? "sockabus-console-v1" : "sockabus-console-api-v1";
const LOCAL_KEYS = ["usedScript", "lastLookup", "bank", "genIds", "verifiedAt"] as const;

export interface Data {
  personas: typeof SEED_PERSONAS;
  convos: Conversation[];
  entities: typeof SEED_ENTITIES;
  pool: number; // how many personas "Generate" has handed out
  telegram: boolean;
  usedScript: number[];
  lastLookup: Lookup | null;
  bank: BankDraft;
  genIds: string[]; // personas made with "Generate persona" in this browser
  verifiedAt: Record<string, number>; // entity id → when this browser's Verify was saved (the API has no verified time)
}

export interface Toast {
  id: number;
  msg: string;
  label?: string;
  href?: string;
  run?: () => void;
  testId?: string; // for the action button
  error?: boolean;
}

type ToastAction = { label: string; href?: string; run?: () => void; testId?: string };

export interface Health {
  ok: boolean;
  status: number; // HTTP status of GET /readyz, 0 = unreachable
  checks: Record<string, string>;
  ms: number;
}

// GET /internal/status (NFR-9): live Telegram connection, recent LLM errors, running workflows.
export interface BotStatus {
  telegram: { connected: boolean; last_message_at: string | null };
  llm: { errors_recent: number };
  workflows: { running: number | null; error?: string };
}

interface UI {
  generating: boolean;
  genError: string | null;
  genErrorSetup: boolean; // 503: the server has no AI model set up, so retrying won't help
  checking: boolean;
  lookupError: string | null;
  typing: Record<string, boolean>;
  lastSync: number;
  toasts: Toast[];
  loaded: boolean; // first API load finished (always true in sample mode)
  loadError: string | null; // last API load failed with this message
  accounts: ApiAccount[]; // honeypot accounts (API mode; Status screen)
  health: Health | null;
  bot: BotStatus | null; // null until loaded, or when /internal/status can't be reached
  justVerified: string | null; // bank account just verified here: drives "Test in bank simulator →"
  saving: Record<string, boolean>; // entity ids whose decision is being sent to the server
  session: SessionStats; // bank simulator, "this demo session" (sessionStorage)
}

export interface SessionStats {
  lookups: number;
  holds: number;
}

export interface State {
  data: Data;
  ui: UI;
}

const seed = (): Data => ({
  personas: structuredClone(SEED_PERSONAS).reverse(), // seed is newest-first; show oldest-first
  convos: structuredClone(SEED_CONVOS),
  entities: structuredClone(SEED_ENTITIES),
  pool: 0,
  telegram: true,
  usedScript: [],
  lastLookup: null,
  bank: { bank: "MBB", acct: "", amt: "150.00" },
  genIds: [],
  verifiedAt: {},
});

const empty = (): Data => ({ ...seed(), personas: [], convos: [], entities: [] });
const initial = () => (USE_SAMPLE ? seed() : empty());

const freshUI = (lastSync: number): UI => ({
  generating: false,
  genError: null,
  genErrorSetup: false,
  checking: false,
  lookupError: null,
  typing: {},
  lastSync,
  toasts: [],
  loaded: USE_SAMPLE,
  loadError: null,
  accounts: [],
  health: null,
  bot: null,
  justVerified: null,
  saving: {},
  session: { lookups: 0, holds: 0 },
});

const SERVER: State = { data: initial(), ui: freshUI(0) };
let state: State | null = null;
const listeners = new Set<() => void>();

const saved = (d: Data): Partial<Data> => (USE_SAMPLE ? d : Object.fromEntries(LOCAL_KEYS.map((k) => [k, d[k]])));

function load(): State {
  let data = initial();
  try {
    const raw = window.localStorage.getItem(KEY);
    if (raw) data = { ...data, ...JSON.parse(raw) };
  } catch {
    // private window or blocked storage: start fresh
  }
  return { data, ui: { ...freshUI(USE_SAMPLE ? Date.now() : 0), session: readSession() } };
}

// Lookups and holds in this browser tab ("this demo session"): sessionStorage, so a new tab starts at 0.
const SESSION_KEY = "sockabus-bank-session";
function readSession(): SessionStats {
  try {
    const j = JSON.parse(window.sessionStorage.getItem(SESSION_KEY) ?? "null") as SessionStats | null;
    if (j && Number.isFinite(j.lookups) && Number.isFinite(j.holds)) return j;
  } catch {
    // blocked storage: count in memory only
  }
  return { lookups: 0, holds: 0 };
}
function countLookup(held: boolean) {
  setUI((u) => {
    const session = { lookups: u.session.lookups + 1, holds: u.session.holds + (held ? 1 : 0) };
    try {
      window.sessionStorage.setItem(SESSION_KEY, JSON.stringify(session));
    } catch {
      // keep it in memory
    }
    return { session };
  });
}

const get = () => (state ??= load());

function set(fn: (s: State) => State, persist = false) {
  state = fn(get());
  if (persist) {
    if (USE_SAMPLE) state = { ...state, ui: { ...state.ui, lastSync: Date.now() } };
    try {
      window.localStorage.setItem(KEY, JSON.stringify(saved(state.data)));
    } catch {
      // storage full or blocked: keep going in memory
    }
  }
  listeners.forEach((l) => l());
}

const setData = (fn: (d: Data) => Data) => set((s) => ({ ...s, data: fn(s.data) }), true);
const setUI = (patch: Partial<UI> | ((u: UI) => Partial<UI>)) =>
  set((s) => ({ ...s, ui: { ...s.ui, ...(typeof patch === "function" ? patch(s.ui) : patch) } }));

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

export function useStore(): State {
  return useSyncExternalStore(subscribe, get, () => SERVER);
}

export const getState = get;

// ---------- derived ----------
export const queueOf = (d: Data) =>
  d.entities
    .map((e, i) => ({ e, i }))
    .filter(({ e }) => e.status === "unverified")
    .sort((a, b) => Number(a.e.sample) - Number(b.e.sample) || b.i - a.i)
    .map(({ e }) => e);
export const liveConvos = (d: Data) => d.convos.filter((c) => c.status !== "closed");
export const activePersonas = (d: Data) => d.personas.filter((p) => p.status === "active");

// ---------- toasts ----------
let toastId = 0;
export function toast(msg: string, action?: ToastAction, opts: { ms?: number; error?: boolean } = {}): number {
  const t: Toast = { id: ++toastId, msg, ...action, error: opts.error };
  setUI((u) => ({ toasts: [...u.toasts, t] }));
  setTimeout(() => dismissToast(t.id), opts.ms ?? 6000);
  return t.id;
}
export const dismissToast = (id: number) => setUI((u) => ({ toasts: u.toasts.filter((t) => t.id !== id) }));

const errMsg = (err: unknown) => (err instanceof Error && err.message ? err.message : "Something went wrong");
const toastError = (err: unknown) => toast(errMsg(err), undefined, { error: true });

// ---------- sync with the Go API ----------
let syncing: Promise<void> | null = null;
const details = new Map<number, { key: string; d: ApiConversationDetail }>(); // per-chat cache
const awaiting = new Map<string, number>(); // web chat: persona id → when the scammer last sent
const REPLY_WAIT_MS = 120_000; // stop showing "typing…" after this
let knownEntities: Set<string> | null = null; // for the "Entity extracted" toast

// One poll of everything the screens show. Concurrent callers share the request in flight.
export function refresh(): Promise<void> {
  if (USE_SAMPLE) return Promise.resolve();
  return (syncing ??= pull().finally(() => (syncing = null)));
}

async function pull() {
  try {
    const [personas, accounts, convos, ents] = await Promise.all([
      api<ApiPersona[]>("/internal/personas"),
      api<ApiAccount[]>("/internal/accounts"),
      api<ApiConversation[]>("/internal/conversations?limit=50"),
      api<ApiEntity[]>("/internal/entities?limit=500"),
    ]);
    // Messages come from GET /internal/conversations/{id}. Refetch a chat only when it changed,
    // or while it is recent (extraction finishes a moment after the message).
    const now = Date.now();
    await Promise.all(
      convos.map(async (c) => {
        const key = `${c.last_message_at}|${c.status}|${c.stage}`;
        const recent = !!c.last_message_at && now - Date.parse(c.last_message_at) < REPLY_WAIT_MS;
        if (details.get(c.id)?.key === key && !recent) return;
        details.set(c.id, { key, d: await api<ApiConversationDetail>(`/internal/conversations/${c.id}`) });
      }),
    );
    const mapped = convos.map((c) => toConversation(c, details.get(c.id)?.d.messages, accounts));
    const typing = { ...get().ui.typing };
    for (const c of mapped) {
      const since = c.web ? awaiting.get(c.personaId) : undefined;
      if (since === undefined) continue;
      const msgs = details.get(Number(c.id))?.d.messages ?? [];
      const answered = msgs.some((m) => m.sender_type !== "scammer" && Date.parse(m.sent_at) >= since);
      typing[c.id] = !answered && c.status === "bot" && now - since < REPLY_WAIT_MS;
      if (!typing[c.id]) awaiting.delete(c.personaId);
    }
    // GET /internal/entities has no scam type or source chat: take both from the earliest chat that captured it.
    const source = new Map<string, { convoId: string; scam: Entity["scam"] }>();
    for (const c of [...convos].sort((a, b) => Date.parse(a.started_at) - Date.parse(b.started_at))) {
      for (const e of details.get(c.id)?.d.entities ?? []) {
        if (!source.has(String(e.id))) source.set(String(e.id), { convoId: String(c.id), scam: c.scam_type?.valid ? c.scam_type.scam_type : "other" });
      }
    }
    const entities = ents.map((e) => withOverlay(toEntity(e, source.get(String(e.id)))));
    const fresh = knownEntities && entities.find((e) => !knownEntities!.has(e.id) && e.status === "unverified");
    if (fresh) toast("Entity extracted · now in the Verify queue", { label: "Open", href: `/verify?selected=${fresh.id}` });
    knownEntities = new Set(entities.map((e) => e.id));
    const genIds = get().data.genIds;
    set((s) => ({
      data: { ...s.data, personas: personas.sort((a, b) => a.id - b.id).map((p) => toPersona(p, accounts, genIds)), convos: mapped, entities },
      ui: { ...s.ui, loaded: true, loadError: null, lastSync: Date.now(), accounts, typing },
    }));
  } catch (err) {
    setUI({ loadError: errMsg(err) });
  }
}

// GET /readyz: 200 or 503 with { postgres, redis, temporal } checks; GET /internal/status alongside.
async function pullHealth() {
  const botReq = api<BotStatus>("/internal/status").catch(() => null);
  const t0 = performance.now();
  let health: Health;
  try {
    const res = await fetch("/api/readyz", { cache: "no-store" });
    const checks = ((await res.json().catch(() => null)) ?? {}) as Record<string, string>;
    // 502/504 with { error } come from our /api proxy when the Go API can't be reached: no check ran.
    const unreachable = !res.ok && (res.status === 502 || res.status === 504) && typeof checks.error === "string";
    health = unreachable
      ? { ok: false, status: 0, checks: {}, ms: Math.round(performance.now() - t0) }
      : { ok: res.ok, status: res.status, checks, ms: Math.round(performance.now() - t0) };
  } catch {
    health = { ok: false, status: 0, checks: {}, ms: Math.round(performance.now() - t0) };
  }
  setUI({ health, bot: await botReq });
}

let syncStarted = false;
// Started by the Shell. Polls every 3 s while the tab is visible (spec S2: 3 s).
export function startSync(): () => void {
  if (USE_SAMPLE || syncStarted) return () => {};
  syncStarted = true;
  void refresh();
  void pullHealth();
  const fast = setInterval(() => !document.hidden && void refresh(), 3000);
  const slow = setInterval(() => !document.hidden && void pullHealth(), 10_000); // spec S6: 10 s
  return () => {
    syncStarted = false;
    clearInterval(fast);
    clearInterval(slow);
  };
}

// ---------- actions ----------
export async function generatePersona(): Promise<string> {
  if (!USE_SAMPLE) {
    setUI({ generating: true, genError: null });
    try {
      const r = await api<ApiGenerateResult>("/internal/personas/generate", { body: { count: 1, mode: "random" } });
      const id = r.personas[0] ? String(r.personas[0].id) : "";
      if (!id) throw new ApiError(r.notes.join(" ") || "The generator returned no persona", 500);
      setData((d) => ({ ...d, genIds: [...d.genIds, id] }));
      await refresh();
      return id;
    } catch (err) {
      setUI({ genError: errMsg(err), genErrorSetup: err instanceof ApiError && err.status === 503 });
      return "";
    } finally {
      setUI({ generating: false });
    }
  }
  setUI({ generating: true, genError: null });
  return new Promise((resolve) =>
    setTimeout(() => {
      let id = "";
      setData((d) => {
        const g = GENERATED[d.pool % GENERATED.length];
        const pool = d.pool + 1;
        id = `p_${40 + pool}`;
        const handle = "@" + g.name.toLowerCase().replace(/[^a-z]/g, "").slice(0, 9) + g.age;
        return { ...d, pool, personas: [...d.personas, { id, ...g, status: "draft", handle, created: "just now", gen: true }] };
      });
      setUI({ generating: false });
      resolve(id);
    }, 1800),
  );
}

export async function setPersonaStatus(id: string, to: PersonaStatus) {
  const p = get().data.personas.find((x) => x.id === id);
  if (!p) return;
  if (!USE_SAMPLE) {
    try {
      await api(`/internal/personas/${id}/status`, { body: { status: to } });
      await refresh();
      toast(`${p.name} is ${to}`);
    } catch (err) {
      toastError(err);
    }
    return;
  }
  setData((d) => ({ ...d, personas: d.personas.map((x) => (x.id === id ? { ...x, status: to } : x)) }));
  toast(`${p.name} is ${to}`);
}

const updateConvo = (id: string, fn: (c: Conversation) => Conversation) =>
  setData((d) => ({ ...d, convos: d.convos.map((c) => (c.id === id ? fn(c) : c)) }));

// mode "human" takes over (the bot stops at once), "bot" hands back.
async function apiTakeover(id: string, mode: "human" | "bot") {
  try {
    await api(`/internal/conversations/${id}/takeover`, { body: { mode } });
    if (mode === "human") setUI((u) => ({ typing: { ...u.typing, [id]: false } }));
    await refresh();
    if (mode === "bot") toast("Bot is replying again");
  } catch (err) {
    toastError(err);
  }
}

export function takeover(id: string) {
  if (!USE_SAMPLE) return void apiTakeover(id, "human");
  updateConvo(id, (c) => ({ ...c, status: "operator" }));
  setUI((u) => ({ typing: { ...u.typing, [id]: false } }));
}

export function handback(id: string) {
  if (!USE_SAMPLE) return void apiTakeover(id, "bot");
  updateConvo(id, (c) => ({ ...c, status: "bot" }));
  toast("Bot is replying again");
}

export function operatorSend(id: string, text: string) {
  const t = clock();
  // Shown at once; the next poll replaces it with the stored message.
  updateConvo(id, (c) => ({ ...c, last: t, msgs: [...c.msgs, { who: "operator", text, t, at: Date.now() }] }));
  if (USE_SAMPLE) return;
  api(`/internal/conversations/${id}/operator-message`, { body: { text } })
    .then(() => {
      details.delete(Number(id));
      return refresh();
    })
    .catch((err: unknown) => {
      updateConvo(id, (c) => ({ ...c, msgs: c.msgs.filter((m) => !(m.who === "operator" && m.text === text && m.t === t)) }));
      toast(`Message not sent: ${errMsg(err)}`, undefined, { error: true });
    });
}

// ---------- Verify / Reject with Undo (spec §4.5) ----------
// The screen changes at once; the save waits UNDO_MS so an undone decision never reaches
// the append-only verifications table. Set NEXT_PUBLIC_UNDO_MS=0 in tests.
const undoEnv = Number(process.env.NEXT_PUBLIC_UNDO_MS);
export const UNDO_MS = process.env.NEXT_PUBLIC_UNDO_MS && undoEnv >= 0 ? undoEnv : 6000;

type Decision = { to: "verified" | "rejected"; note: string };
type EntityFields = Pick<Entity, "status" | "conf" | "note">;
interface Pending extends Decision {
  prev: EntityFields;
  timer: ReturnType<typeof setTimeout>;
  toastId: number;
}
const pending = new Map<string, Pending>(); // one timer per entity

const patchEntity = (id: string, fields: Partial<Entity>) =>
  setData((d) => ({ ...d, entities: d.entities.map((x) => (x.id === id ? { ...x, ...fields } : x)) }));
const applyDecision = (id: string, { to, note }: Decision) =>
  patchEntity(id, { status: to, conf: to === "verified" ? "high" : "low", note });

// Decisions the server hasn't confirmed yet, laid over each poll so a row doesn't jump back
// into the queue. Infinity while waiting / sending; a short grace period after the save.
const overlay = new Map<string, { d: Decision; until: number }>();
const SETTLE_MS = 15_000;

export function withOverlay(e: Entity): Entity {
  const o = overlay.get(e.id);
  if (!o) return e;
  if ((o.until !== Infinity && e.status === o.d.to) || Date.now() > o.until) {
    overlay.delete(e.id);
    return e;
  }
  return { ...e, status: o.d.to, conf: o.d.to === "verified" ? "high" : "low", note: o.d.note };
}

// POST /internal/entities/{id}/verify with { decision, note }. keepalive lets the request finish
// while the tab closes. Throws with the API's error message on failure.
async function saveDecision(id: string, d: Decision, keepalive = false): Promise<void> {
  if (USE_SAMPLE) return; // sample mode: the change is already in local data
  await api(`/internal/entities/${id}/verify`, { body: { decision: d.to, note: d.note }, keepalive });
}

export function decide(id: string, to: Decision["to"], note = "") {
  const e = get().data.entities.find((x) => x.id === id);
  if (!e) return;
  if (pending.has(id)) send(id); // a second decision on the same entity: save the first one now
  const prev = { status: e.status, conf: e.conf, note: e.note };
  applyDecision(id, { to, note });
  overlay.set(id, { d: { to, note }, until: Infinity });
  if (e.type === "bank_account") setUI((u) => ({ justVerified: to === "verified" ? id : u.justVerified === id ? null : u.justVerified }));
  const word = to === "verified" ? "Verified" : "Rejected";
  const toastId = toast(
    `${word} ${typeLabel(e)} ${showVal(e)}${to === "verified" ? " — now high" : ""}`,
    { label: "Undo", run: () => undoDecision(id), testId: "verify-undo-btn" },
    { ms: Math.max(6000, UNDO_MS) },
  );
  pending.set(id, { to, note, prev, toastId, timer: setTimeout(() => send(id), UNDO_MS) });
}

function undoDecision(id: string) {
  const p = pending.get(id);
  if (!p) return; // already sent
  clearTimeout(p.timer);
  pending.delete(id);
  overlay.delete(id);
  patchEntity(id, p.prev);
  setUI((u) => ({ justVerified: u.justVerified === id ? null : u.justVerified }));
  toast("Undone");
}

function send(id: string, keepalive = false) {
  const p = pending.get(id);
  if (!p) return;
  clearTimeout(p.timer);
  pending.delete(id);
  // Too late to undo: drop the Undo button from its toast.
  setUI((u) => ({ toasts: u.toasts.map((t) => (t.id === p.toastId ? { ...t, run: undefined, label: undefined } : t)) }));
  commit(id, p, p.prev, keepalive);
}

// Sends a decision. On failure the row goes back to the queue with a Retry that resends it.
const setSaving = (id: string, on: boolean) => setUI((u) => ({ saving: { ...u.saving, [id]: on } }));

function commit(id: string, d: Decision, prev: EntityFields, keepalive = false) {
  overlay.set(id, { d, until: Infinity });
  setSaving(id, true);
  saveDecision(id, d, keepalive)
    .then(() => {
      overlay.set(id, { d, until: Date.now() + SETTLE_MS });
      setSaving(id, false);
      if (d.to === "verified") setData((x) => ({ ...x, verifiedAt: { ...x.verifiedAt, [id]: Date.now() } }));
      void refresh();
    })
    .catch((err: unknown) => {
      overlay.delete(id);
      setSaving(id, false);
      patchEntity(id, prev);
      toast(
        errMsg(err),
        {
          label: "Retry",
          testId: "verify-retry-btn",
          run: () => {
            applyDecision(id, d);
            commit(id, d, prev);
          },
        },
        { error: true },
      );
    });
}

// Send one entity's waiting decision now (bank simulator: test it without waiting for the Undo window).
export const sendDecisionNow = (id: string) => send(id);
export const isDecisionWaiting = (id: string) => pending.has(id);

// Send every waiting decision now: on route change and when the tab closes, so none is lost.
export function flushDecisions(keepalive = false) {
  for (const id of [...pending.keys()]) send(id, keepalive);
}
if (typeof window !== "undefined") window.addEventListener("pagehide", () => flushDecisions(true));

// Web chat (dev only): the backend's chat simulator plays a Telegram account for the persona.
const SIM_SCAMMER = "hr_parttime_job"; // shows as @sim_hr_parttime_job
const simAccounts = new Map<string, number>(); // persona id → simulated account id

async function apiScammerSends(personaId: string, text: string) {
  try {
    let account = simAccounts.get(personaId);
    if (account === undefined) {
      const r = await api<{ account_id: number }>("/internal/sim/accounts", { body: { persona_id: Number(personaId) } });
      account = r.account_id;
      simAccounts.set(personaId, account);
    }
    await api("/internal/sim/messages", { body: { account_id: account, from: SIM_SCAMMER, text } });
    awaiting.set(personaId, Date.now());
    await refresh();
    setTimeout(() => void refresh(), 1000); // the message is stored by a workflow, a moment later
  } catch (err) {
    toastError(err);
  }
}

// Scammer message from the web chat page. Returns the conversation id (sample mode only).
export function scammerSends(personaId: string, text: string): string | null {
  if (!USE_SAMPLE) {
    void apiScammerSends(personaId, text);
    return null;
  }
  const d = get().data;
  const p = d.personas.find((x) => x.id === personaId);
  if (!p) return null;
  const t = clock();
  const kind = classify(text);
  const found = extract(text);
  let c = d.convos.find((x) => x.personaId === p.id && x.web);
  const isNew = !c;
  c ??= { id: `c_${1040 + d.convos.length}`, scammer: "@hr_parttime_job", personaId: p.id, scam: "other", status: "bot", stage: 0, last: t, msgs: [], web: true };
  const scam = c.scam === "other" && kind ? kind : c.scam;
  const stage = /deposit|pay|transfer|fee|bank in/i.test(text) ? Math.max(c.stage, 2) : c.stage;
  const convo: Conversation = { ...c, scam, stage, last: t, msgs: [...c.msgs, { who: "scammer", text, t, at: Date.now() }] };

  const entities = [...d.entities];
  let newId: string | null = null;
  for (const f of found) {
    const i = entities.findIndex((x) => x.type === f.type && x.bank === f.bank && x.value === f.value);
    if (i >= 0) {
      entities[i] = { ...entities[i], sightings: entities[i].sightings + 1, convoId: entities[i].convoId ?? convo.id };
    } else {
      newId = `e_${Math.random().toString(36).slice(2, 7)}`;
      entities.push({ id: newId, ...f, scam: scam === "other" ? (kind ?? "other") : scam, status: "unverified", conf: "medium", sightings: 1, first: `${today()}, ${t}`, firstAt: Date.now(), convoId: convo.id, stageAt: Math.max(stage, 2), sample: false });
    }
  }
  setData((x) => ({ ...x, entities, convos: isNew ? [convo, ...x.convos] : x.convos.map((y) => (y.id === convo.id ? convo : y)) }));
  if (newId) toast("Entity extracted · now in the Verify queue", { label: "Open", href: `/verify?selected=${newId}` });

  if (convo.status === "bot") {
    setUI((u) => ({ typing: { ...u.typing, [convo.id]: true } }));
    const hasAcct = found.some((f) => f.type === "bank_account" || f.type === "wallet");
    // Real persona waits 10–30 s; the demo waits ~2 s.
    setTimeout(() => {
      setUI((u) => ({ typing: { ...u.typing, [convo.id]: false } }));
      const cur = get().data.convos.find((x) => x.id === convo.id);
      if (!cur || cur.status !== "bot") return;
      const [reply, next] = personaReply(cur, text, hasAcct);
      const rt = clock();
      updateConvo(cur.id, (x) => ({ ...x, stage: next, last: rt, msgs: [...x.msgs, { who: "persona", text: reply, t: rt, at: Date.now() }] }));
    }, 1600 + Math.random() * 1200);
  }
  return convo.id;
}

export const markScript = (i: number) =>
  setData((d) => (d.usedScript.includes(i) ? d : { ...d, usedScript: [...d.usedScript, i] }));

export const setBankDraft = (patch: Partial<BankDraft>) => setData((d) => ({ ...d, bank: { ...d.bank, ...patch } }));

export function fillBank(bank: string, acct: string) {
  setData((d) => ({ ...d, bank: { ...d.bank, bank, acct: groupAcct(acct) }, lastLookup: null }));
}

export const clearLookup = () => {
  setData((d) => ({ ...d, lastLookup: null }));
  setUI({ lookupError: null });
};

// Through the Next server route, so the lookup token never reaches the browser. Two results (D1).
async function apiLookup() {
  setUI({ checking: true, lookupError: null });
  try {
    const { bank, acct } = get().data.bank;
    const r = await api<ApiLookupResult>("/demo/lookup", { body: { bank, account: acct } });
    setData((x) => ({ ...x, lastLookup: { result: r.res.match ? "held" : "sent", entityId: null, res: r.res, bank: x.bank.bank, acct: digits(acct), amt: x.bank.amt, ms: r.ms } }));
    countLookup(r.res.match);
    noteHeld();
  } catch (err) {
    setData((x) => ({ ...x, lastLookup: null }));
    setUI({ lookupError: errMsg(err) });
  } finally {
    setUI({ checking: false });
  }
}

export function runLookup() {
  const b = get().data.bank;
  if (isStoryAccount(b.bank, digits(b.acct))) return storyLookup();
  if (!USE_SAMPLE) return void apiLookup();
  setUI({ checking: true });
  setTimeout(() => {
    const d = get().data;
    const r = lookup(d.entities, d.bank.bank, d.bank.acct);
    setData((x) => ({ ...x, lastLookup: { ...r, bank: x.bank.bank, acct: digits(x.bank.acct), amt: x.bank.amt } }));
    countLookup(r.result === "held");
    noteHeld();
    setUI({ checking: false });
  }, 650);
}

// The bank story's fake DemoBank account is answered here from scripted data built with the live
// API's rules (./risk), in both modes: it is never sent to Sockabus, so no fake record is needed there.
function storyLookup() {
  setUI({ checking: true, lookupError: null });
  setTimeout(() => {
    flagStoryAccount(); // a transfer before step 1 still tells the story: Sockabus flagged it first
    const res = storyResponse(getBankDemo().flaggedAt ?? Date.now());
    setData((x) => ({ ...x, lastLookup: { result: "held", entityId: null, res, bank: x.bank.bank, acct: digits(x.bank.acct), amt: x.bank.amt, ms: 38, scripted: true } }));
    countLookup(true);
    noteHeld();
    setUI({ checking: false });
  }, 450);
}

// A held transfer goes to the mock fraud console's review queue (a person decides; never auto-frozen).
function noteHeld() {
  const L = get().data.lastLookup;
  if (L?.result !== "held") return;
  const amt = parseFloat(L.amt.replace(/[^\d.]/g, "")) || 0;
  recordHeld({
    bank: L.bank,
    acctMasked: maskDigits(L.acct ?? ""),
    amt,
    score: L.res.risk_score ?? 0,
    action: L.res.recommended_action ?? "HOLD_FOR_REVIEW",
    story: !!L.scripted,
    sameBank: L.bank === STORY_BANK,
  });
}

export function cancelTransfer() {
  setData((d) => ({ ...d, bank: { ...d.bank, acct: "" } }));
  toast("Transfer cancelled. Money stayed with the customer");
}

export function toggleTelegram() {
  const up = !get().data.telegram;
  setData((d) => ({ ...d, telegram: up }));
  toast(up ? "Telegram reconnected · recovery alert sent" : "Alert sent to team Telegram group and email");
}

export function resetDemo() {
  resetBankDemo(); // the bank simulator's mock fraud console and story progress (this browser only)
  if (!USE_SAMPLE) {
    // Server data stays. Send waiting decisions (never lose one), then clear this browser's demo state.
    flushDecisions();
    const s = seed();
    setData((d) => ({ ...d, usedScript: s.usedScript, lastLookup: null, bank: s.bank, genIds: [], verifiedAt: {} }));
    setUI({ lookupError: null, genError: null });
    toast("Demo state in this browser reset. Server data is unchanged");
    return;
  }
  for (const p of pending.values()) clearTimeout(p.timer);
  pending.clear();
  overlay.clear();
  set((s) => ({ data: seed(), ui: { ...freshUI(Date.now()), session: s.ui.session } }), true);
  toast("Demo data reset");
}


export const clearJustVerified = () => setUI({ justVerified: null });
