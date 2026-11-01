// Shared helpers for the Playwright API and UI tests (TRD §9).
import { expect, request, type APIRequestContext } from "@playwright/test";

export const API_URL = process.env.SOCKABUS_API_URL ?? "http://localhost:8080";

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
  { timeoutMs = 30_000, everyMs = 500 } = {},
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
  throw new Error(`timed out after ${timeoutMs} ms waiting on GET ${path}; last body: ${JSON.stringify(last).slice(0, 500)}`);
}

// A 12-digit Maybank-length account number no earlier run used, so every run creates a new entity.
export function freshAccountNumber(): string {
  const tail = `${Date.now()}`.slice(-9);
  return `5${Math.floor(Math.random() * 90 + 10)}${tail}`;
}

// Matches the backend's grouping: "5621 0384 1192".
export const group4 = (digits: string) => digits.replace(/(\d{4})(?=\d)/g, "$1 ");

// Response shapes (internal/server/live.go, internal/honeypot).
export interface Conversation {
  id: number;
  scammer_handle: string;
  status: "bot" | "human" | "closed";
  persona_id: number;
}
export interface Message {
  id: number;
  direction: "inbound" | "outbound";
  sender_type: "scammer" | "persona" | "operator";
  text: string;
}
export interface Entity {
  id: number;
  type: string;
  value_normalized: string;
  status: "unverified" | "verified" | "rejected";
  confidence: "low" | "medium" | "high";
}
export interface ConversationDetail {
  conversation: Conversation;
  messages: Message[];
  entities: Entity[];
}
