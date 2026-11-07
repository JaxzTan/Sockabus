// Shared helpers for the Playwright API and UI tests (TRD §9).
import { execFileSync } from "node:child_process";
import { createHash, createHmac } from "node:crypto";
import { expect, request, type APIRequestContext, type Page } from "@playwright/test";

export const API_URL = process.env.SOCKABUS_API_URL ?? "http://localhost:8080";
export const WEB_URL = process.env.SOCKABUS_WEB_URL ?? "http://localhost:3000";

export function operatorToken(): string {
  const token = process.env.SOCKABUS_OPERATOR_TOKEN;
  if (!token) throw new Error("SOCKABUS_OPERATOR_TOKEN must be set to the backend's operator token");
  return token;
}

// A request context that sends the operator bearer token on every call.
export function operatorContext(): Promise<APIRequestContext> {
  return request.newContext({ baseURL: API_URL, extraHTTPHeaders: { Authorization: `Bearer ${operatorToken()}` } });
}

// GET path until pick(body) returns a value (not undefined/null/false), then return it.
// Fails with the last body when timeoutMs runs out.
export async function pollFor<B, T>(
  api: APIRequestContext,
  path: string,
  pick: (body: B) => T | undefined | null | false,
  { timeoutMs = 30_000, everyMs = 500, what = "" } = {},
): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  let last: unknown;
  while (Date.now() < deadline) {
    const res = await api.get(path);
    expect(res.ok(), `GET ${path} → ${res.status()}`).toBeTruthy();
    last = await res.json();
    const got = pick(last as B);
    if (got !== undefined && got !== null && got !== false) return got;
    await new Promise((r) => setTimeout(r, everyMs));
  }
  throw new Error(`timed out after ${timeoutMs} ms waiting ${what ? `for ${what} ` : ""}on GET ${path}; last body: ${JSON.stringify(last).slice(0, 800)}`);
}

// A 12-digit Maybank-length account number no earlier run used, so every run creates a new entity.
// "5" + 8 clock digits + a per-process sequence (2 digits) + 1 random digit: two calls in the same
// millisecond never collide (an earlier version did, 1 time in 90, and a "clean" account came back as a hit).
let accountSeq = 0;
export function freshAccountNumber(): string {
  const clock = `${Date.now()}`.slice(-8);
  const seq = String(accountSeq++ % 100).padStart(2, "0");
  return `5${clock}${seq}${Math.floor(Math.random() * 10)}`;
}

// A scammer name unique to this run (letters, digits, underscore: what the simulator keeps).
export const freshScammer = (tag: string) => `e2e_${tag}_${Date.now().toString(36)}${Math.floor(Math.random() * 1e4)}`;

// Matches the backend's grouping: "5621 0384 1192".
export const group4 = (digits: string) => digits.replace(/(\d{4})(?=\d)/g, "$1 ");

// Response shapes (internal/server/live.go, internal/honeypot).
export interface Conversation {
  id: number;
  account_id: number;
  scammer_handle: string;
  status: "bot" | "human" | "closed";
  stage: string;
  scam_type: { scam_type: string; valid: boolean };
  temporal_workflow_id: string;
  persona_id: number;
  channel: string;
  persona_name: string;
}
export interface Message {
  id: number;
  direction: "inbound" | "outbound";
  sender_type: "scammer" | "persona" | "operator";
  text: string;
  sent_at: string;
  undecryptable?: boolean;
}
export interface Entity {
  id: number;
  type: string;
  value_normalized: string;
  bank_or_chain: string | null;
  status: "unverified" | "verified" | "rejected";
  confidence: "low" | "medium" | "high";
  sightings_count: number;
  first_seen: string;
  last_seen: string;
}
export interface ConversationDetail {
  conversation: Conversation;
  messages: Message[];
  entities: Entity[];
}
export interface Persona {
  id: number;
  name: string;
  age: number | null;
  language_mix: Record<string, number> | null;
  backstory: string | null;
  wealth_profile: Record<string, unknown> | null;
  profile: Record<string, unknown> | null;
  status: "draft" | "active" | "paused" | "retired";
  prompt_version: number;
  scam_types: string[];
  created_at: string;
}
export interface Account {
  id: number;
  persona_id: number;
  channel: "telegram" | "whatsapp";
  phone: string;
  session_ref: string;
  status: "active" | "banned" | "cooldown";
}

// Generate one persona with the real LLM and activate it. Returns the persona id.
export async function generateActivePersona(api: APIRequestContext): Promise<number> {
  const gen = await api.post("/internal/personas/generate", { data: { count: 1, mode: "random" }, timeout: 300_000 });
  const body = await gen.text();
  expect(gen.status(), `POST /internal/personas/generate → ${gen.status()} ${body.slice(0, 300)}`).toBe(201);
  const id = (JSON.parse(body) as { personas: { id: number }[] }).personas[0]?.id;
  expect(id, "generate returned no persona").toBeGreaterThan(0);
  const act = await api.post(`/internal/personas/${id}/status`, { data: { status: "active" } });
  expect(act.ok(), `activate persona ${id} → ${act.status()}`).toBeTruthy();
  return id;
}

// Close every open conversation of a persona and retire it, so reruns start clean.
export async function retirePersona(api: APIRequestContext, personaId: number) {
  if (!personaId) return;
  const list = (await (await api.get("/internal/conversations?limit=500")).json()) as Conversation[];
  for (const c of list.filter((x) => x.persona_id === personaId && x.status !== "closed")) {
    await api.post(`/internal/conversations/${c.id}/close`, { data: {} });
  }
  await api.post(`/internal/personas/${personaId}/status`, { data: { status: "retired" } });
}

// Console sign-in is Google only, which a test can't drive. Tests mint the same session cookie the
// callback sets (web/src/lib/auth.ts makeSession): "<b64url email>.<expiry ms>.<HMAC>", keyed by
// SOCKABUS_SESSION_SECRET. The email must be on the console's allowlist (SOCKABUS_CONSOLE_GOOGLE_ALLOWED_EMAILS).
export const CONSOLE_EMAIL = (process.env.SOCKABUS_E2E_CONSOLE_EMAIL ?? "e2e-operator@sockabus.test").toLowerCase();
export function consoleSessionCookie(): { name: string; value: string } {
  const secret = process.env.SOCKABUS_SESSION_SECRET ?? "";
  expect(secret.length >= 32, "console sign-in is on: set SOCKABUS_SESSION_SECRET (same as the console's)").toBeTruthy();
  const payload = `${Buffer.from(CONSOLE_EMAIL).toString("base64url")}.${Date.now() + 60 * 60 * 1000}`;
  const key = createHash("sha256").update("sockabus-console:session:" + secret).digest();
  return { name: "sockabus_session", value: `${payload}.${createHmac("sha256", key).update(payload).digest("base64url")}` };
}

// Sign in to the console when its gate is on. Fails (never skips) when the session secret is missing.
export async function signIn(page: Page, path: string) {
  await page.goto(path);
  if (!page.url().includes("/sign-in")) return;
  await page.context().addCookies([{ ...consoleSessionCookie(), url: new URL(page.url()).origin, httpOnly: true, sameSite: "Lax" }]);
  await page.goto(path);
  await expect(page).not.toHaveURL(/\/sign-in/);
}

// A persona built from fields through POST /internal/personas (no LLM call), active, with playbooks.
// Replies to scammers still come from the real persona LLM.
export async function createActivePersona(api: APIRequestContext, tag: string, scamTypes: string[] = ["job"]): Promise<Persona> {
  const res = await api.post("/internal/personas", {
    data: {
      name: `E2E ${tag} ${Date.now().toString(36)}`,
      age: 61,
      language_mix: { en: 0.6, ms: 0.3, nan: 0.1 },
      backstory: "Retired school canteen operator in Ipoh. Widowed, two grown children in KL, keeps savings in FD, types slowly on WhatsApp and Telegram.",
      wealth_profile: { savings_rm: 85000, income_bracket: "B40", banks: ["Maybank", "Public Bank"], crypto_familiarity: "none" },
      profile: { city: "Ipoh", state: "Perak", occupation: "Retired canteen operator", gender: "female", ethnicity: "Chinese" },
      status: "active",
      scam_types: scamTypes,
    },
  });
  expect(res.status(), `POST /internal/personas → ${res.status()} ${await res.text()}`).toBe(201);
  return (await res.json()) as Persona;
}

export async function simAccountFor(api: APIRequestContext, personaId: number): Promise<number> {
  const res = await api.post("/internal/sim/accounts", { data: { persona_id: personaId } });
  expect(res.ok(), `POST /internal/sim/accounts → ${res.status()} ${await res.text()}`).toBeTruthy();
  return ((await res.json()) as { account_id: number }).account_id;
}

export async function simSend(api: APIRequestContext, accountId: number, from: string, text: string) {
  const res = await api.post("/internal/sim/messages", { data: { account_id: accountId, from, text } });
  expect(res.status(), `POST /internal/sim/messages → ${res.status()} ${await res.text()}`).toBe(202);
}

// Wait until the conversation holds more than `after` persona messages; returns the new ones.
export async function waitForPersonaReplies(api: APIRequestContext, convId: number, after: number, timeoutMs = 90_000): Promise<Message[]> {
  const persona = await pollFor<ConversationDetail, Message[]>(
    api,
    `/internal/conversations/${convId}`,
    (d) => {
      const p = d.messages.filter((m) => m.sender_type === "persona");
      return p.length > after ? p : undefined;
    },
    { timeoutMs, everyMs: 1000, what: `persona reply #${after + 1} in conversation ${convId}` },
  );
  return persona.slice(after);
}

// The stage example lines the backend sends instead of a model reply when the persona LLM fails
// (internal/honeypot/activities.go fallbackReply: the playbook stage's example, or playbook.Generic's).
const CANNED = [
  "hello.. who is this ah?",
  "ok can explain again? how it works ah",
  "ok la.. which account ah? name what?",
  "bank say cannot transfer to this one, got other account?",
];

// Failed LLM calls in the backend's recent window (GET /internal/status).
export async function llmErrors(api: APIRequestContext): Promise<number> {
  return ((await (await api.get("/internal/status")).json()) as { llm: { errors_recent: number } }).llm.errors_recent;
}
export async function cannedLines(api: APIRequestContext): Promise<Set<string>> {
  const pbs = (await (await api.get("/internal/playbooks")).json()) as { stages: { example: string }[] }[];
  return new Set([...CANNED, ...pbs.flatMap((p) => p.stages.map((s) => s.example))]);
}

// Run SQL directly on the test stack's database, for setup that no API can do (e.g. corrupting a stored
// ciphertext). SOCKABUS_E2E_PSQL is a psql command reading SQL on stdin: playwright/scripts/e2e-stack.sh env sets
// "docker exec -i sockabus-e2e-pg psql -U buru -d sockabus_e2e"; CI sets the Compose equivalent.
// Fails (never skips) when it is missing, and refuses anything that mentions Supabase.
export function psql(sql: string): string {
  const cmd = process.env.SOCKABUS_E2E_PSQL ?? "";
  if (!cmd) throw new Error("SOCKABUS_E2E_PSQL is not set: this test writes to the test database directly (eval \"$(playwright/scripts/e2e-stack.sh env)\")");
  if (/supabase/i.test(cmd) || /supabase/i.test(sql)) throw new Error("SOCKABUS_E2E_PSQL must point at the throwaway test database, never Supabase");
  const [bin, ...args] = cmd.trim().split(/\s+/);
  return execFileSync(bin, [...args, "-v", "ON_ERROR_STOP=1", "-qtA"], { input: sql, encoding: "utf8" }).trim();
}
