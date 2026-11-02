// One test per HTTP route: the Go API (internal/server/server.go, internal/server/live.go) and the
// console's own API routes (web/src/app/api/**/route.ts). Each test checks the status code, that the
// route refuses a caller without credentials (where auth applies), and the response shape.
// Everything runs against the real backend and database: fixtures are created through the API with
// values generated per run, and nothing is mocked.
import { expect, request, test, type APIRequestContext } from "@playwright/test";
import {
  type Account,
  API_URL,
  type Conversation,
  type ConversationDetail,
  type Entity,
  freshAccountNumber,
  freshScammer,
  group4,
  operatorContext,
  type Persona,
  pollFor,
  retirePersona,
  WEB_URL,
} from "../helpers";

// Not serial: a failing route must not stop the others from running. Each test sets up what it needs
// on top of the shared fixtures below (beforeAll runs again if a failure restarts the worker).

const run = `${Date.now()}`;
let api: APIRequestContext; // with the operator token
let anon: APIRequestContext; // no token
let wrong: APIRequestContext; // a wrong token

// Fixtures shared by the route tests, created through the API in beforeAll.
let personaId = 0;
let simAccountId = 0;
let conversationId = 0;
let account = "";
const scammer = freshScammer("ep");

// Every authenticated Go route must answer 401 with {"error": ...} without a token or with a wrong one.
async function expectRejected(method: "GET" | "POST" | "PUT" | "DELETE", path: string, data?: unknown) {
  for (const [who, ctx] of [
    ["no token", anon],
    ["wrong token", wrong],
  ] as const) {
    const res = await ctx.fetch(path, { method, data });
    expect(res.status(), `${method} ${path} with ${who}`).toBe(401);
    expect(await res.json()).toEqual({ error: expect.stringContaining("bearer token") });
  }
}

const isoDate = expect.stringMatching(/^\d{4}-\d{2}-\d{2}T/);

test.beforeAll(async () => {
  api = await operatorContext();
  anon = await request.newContext({ baseURL: API_URL });
  wrong = await request.newContext({ baseURL: API_URL, extraHTTPHeaders: { Authorization: "Bearer not-the-operator-token-000000000" } });

  // A persona built from fields (no LLM), active, with the job playbook.
  const p = await api.post("/internal/personas", {
    data: {
      name: `E2E Fixture ${run}`,
      age: 58,
      language_mix: { en: 0.6, ms: 0.4 },
      backstory: "Retired clerk from Ipoh, checks messages slowly.",
      status: "active",
      scam_types: ["job"],
      system_prompt: "You are a retired clerk from Ipoh. Reply briefly in Manglish.",
    },
  });
  expect(p.status(), await p.text()).toBe(201);
  personaId = ((await p.json()) as Persona).id;

  const sa = await api.post("/internal/sim/accounts", { data: { persona_id: personaId } });
  expect(sa.ok(), await sa.text()).toBeTruthy();
  simAccountId = ((await sa.json()) as { account_id: number }).account_id;

  account = freshAccountNumber();
  const sent = await api.post("/internal/sim/messages", {
    data: { account_id: simAccountId, from: scammer, text: `Part time job RM300 a day. Transfer RM50 deposit to Maybank ${group4(account)} first` },
  });
  expect(sent.status(), await sent.text()).toBe(202);
  conversationId = await pollFor<Conversation[], number>(
    api,
    "/internal/conversations?limit=500",
    (l) => l.find((c) => c.scammer_handle === `@sim_${scammer}`)?.id,
    { what: "the fixture conversation" },
  );
});

// The fixture's bank account, once the workflow has extracted it.
const fixtureEntity = () =>
  pollFor<Entity[], Entity>(api, "/internal/entities?limit=500", (l) => l.find((x) => x.type === "bank_account" && x.value_normalized === account), {
    timeoutMs: 60_000,
    what: `the extracted bank account ${account}`,
  });

test.afterAll(async () => {
  await retirePersona(api, personaId);
  await Promise.all([api.dispose(), anon.dispose(), wrong.dispose()]);
});

// ───────────────────────── server.go ─────────────────────────

test("GET /healthz", async () => {
  const res = await anon.get("/healthz"); // public by design: liveness probe
  expect(res.status()).toBe(200);
  expect(res.headers()["content-type"]).toContain("application/json");
  expect(await res.json()).toEqual({ status: "ok" });
});

test("GET /readyz", async () => {
  const res = await anon.get("/readyz"); // public by design: readiness probe
  expect(res.status()).toBe(200);
  expect(await res.json()).toEqual({ postgres: "ok", redis: "ok", temporal: "ok" });
});

// ───────────────────────── live.go: lookup ─────────────────────────

test("POST /v1/lookup", async () => {
  await expectRejected("POST", "/v1/lookup", { type: "bank_account", value: account });

  const miss = await api.post("/v1/lookup", { data: { type: "bank_account", value: freshAccountNumber(), bank: "MBB" } });
  expect(miss.status()).toBe(200);
  expect(await miss.json()).toEqual({ match: false });

  const bad = await api.post("/v1/lookup", { data: { type: "nope", value: "1" } });
  expect(bad.status()).toBe(400);
  expect(await bad.json()).toEqual({ error: expect.stringContaining("type must be") });

  const unknownField = await api.post("/v1/lookup", { data: { type: "bank_account", value: account, extra: 1 } });
  expect(unknownField.status()).toBe(400);

  // Lookup policy A: the fixture account is extracted but not yet verified, so a bank gets no match.
  // (The verify test below checks the same account matches with high confidence once verified.)
  const e = await fixtureEntity();
  expect(e.status).toBe("unverified");
  const unverified = await api.post("/v1/lookup", { data: { type: "bank_account", value: account, bank: "MBB" } });
  expect(unverified.status()).toBe(200);
  expect(await unverified.json(), "an unverified bank account never matches (policy A)").toEqual({ match: false });
});

test("POST /v1/lookup: policy A (unverified phone matches, unverified bank account does not)", async () => {
  const d = run.slice(-7);
  const phone = `012-${d.slice(0, 3)} ${d.slice(3)}`;
  const acct = freshAccountNumber();
  const from = freshScammer("pa");
  const sent = await api.post("/internal/sim/messages", {
    data: { account_id: simAccountId, from, text: `Call my agent ${phone} then transfer RM50 to Maybank ${group4(acct)}` },
  });
  expect(sent.status(), await sent.text()).toBe(202);
  await pollFor<Entity[], Entity>(api, "/internal/entities?limit=500", (l) => l.find((x) => x.type === "bank_account" && x.value_normalized === acct), {
    timeoutMs: 60_000,
    what: `the extracted bank account ${acct}`,
  });
  const ph = await pollFor<Entity[], Entity>(api, "/internal/entities?limit=500", (l) => l.find((x) => x.type === "phone" && x.value_normalized === `+6012${d}`), {
    timeoutMs: 60_000,
    what: `the extracted phone +6012${d}`,
  });
  expect(ph.status).toBe("unverified");

  const phoneHit = (await (await api.post("/v1/lookup", { data: { type: "phone", value: phone } })).json()) as Record<string, unknown>;
  expect(phoneHit, "an unverified phone still matches, tagged unverified").toMatchObject({ match: true, status: "unverified", first_seen: isoDate });
  expect(phoneHit.confidence, "unverified is never high (FR-9)").not.toBe("high");

  expect(await (await api.post("/v1/lookup", { data: { type: "bank_account", value: acct, bank: "MBB" } })).json()).toEqual({ match: false });
});

// ───────────────────────── live.go: personas ─────────────────────────

test("GET /internal/personas", async () => {
  await expectRejected("GET", "/internal/personas");
  const res = await api.get("/internal/personas");
  expect(res.status()).toBe(200);
  const list = (await res.json()) as Persona[];
  expect(Array.isArray(list)).toBe(true);
  const mine = list.find((p) => p.id === personaId);
  expect(mine, `fixture persona ${personaId} listed`).toBeTruthy();
  expect(mine).toMatchObject({
    name: `E2E Fixture ${run}`,
    age: 58,
    language_mix: { en: 0.6, ms: 0.4 },
    status: "active",
    prompt_version: 1,
    scam_types: ["job"],
    created_at: isoDate,
  });
});

test("POST /internal/personas", async () => {
  await expectRejected("POST", "/internal/personas", { name: "x", language_mix: { en: 1 } });
  const res = await api.post("/internal/personas", { data: { name: `E2E Create ${run}`, age: 41, language_mix: { en: 1 } } });
  expect(res.status()).toBe(201);
  const p = (await res.json()) as Persona;
  expect(p).toMatchObject({ id: expect.any(Number), name: `E2E Create ${run}`, age: 41, status: "draft", prompt_version: 0, scam_types: [] });

  const bad = await api.post("/internal/personas", { data: { name: "", language_mix: { en: 1 } } });
  expect(bad.status()).toBe(400);
  expect(await bad.json()).toEqual({ error: expect.stringContaining("name is required") });
  await retirePersona(api, p.id);
});

test("POST /internal/personas/generate", async () => {
  test.setTimeout(330_000);
  await expectRejected("POST", "/internal/personas/generate", { count: 1 });
  const res = await api.post("/internal/personas/generate", { data: { count: 1, mode: "random" }, timeout: 320_000 });
  const text = await res.text();
  expect(res.status(), text.slice(0, 400)).toBe(201);
  const body = JSON.parse(text) as { personas: { id: number; name: string; age: number; status: string; summary: string }[]; notes: string[] | null };
  expect(body.personas).toHaveLength(1);
  expect(body.personas[0]).toEqual({ id: expect.any(Number), name: expect.any(String), age: expect.any(Number), status: "draft", summary: expect.any(String) });
  expect(body.personas[0].name.length).toBeGreaterThan(1);
  await retirePersona(api, body.personas[0].id);

  const bad = await api.post("/internal/personas/generate", { data: { count: 1, unknown: true } });
  expect(bad.status()).toBe(400);
});

test("POST /internal/personas/{id}/status", async () => {
  await expectRejected("POST", `/internal/personas/${personaId}/status`, { status: "paused" });
  const res = await api.post(`/internal/personas/${personaId}/status`, { data: { status: "paused" } });
  expect(res.status()).toBe(200);
  expect(await res.json()).toEqual({ id: personaId, status: "paused" });
  const back = await api.post(`/internal/personas/${personaId}/status`, { data: { status: "active" } });
  expect(back.status()).toBe(200);

  expect((await api.post(`/internal/personas/${personaId}/status`, { data: { status: "zombie" } })).status()).toBe(400);
  expect((await api.post(`/internal/personas/abc/status`, { data: { status: "active" } })).status()).toBe(400);
  expect((await api.post(`/internal/personas/999999999/status`, { data: { status: "active" } })).status(), "unknown persona id").toBe(404);
});

test("POST /internal/personas/{id}/prompt", async () => {
  await expectRejected("POST", `/internal/personas/${personaId}/prompt`, { system_prompt: "x" });
  const prompt = `E2E prompt ${run}: you are a retired clerk.`;
  const res = await api.post(`/internal/personas/${personaId}/prompt`, { data: { system_prompt: prompt } });
  expect(res.status()).toBe(201);
  expect(await res.json()).toMatchObject({ id: expect.any(Number), persona_id: personaId, version: 2, system_prompt: prompt, created_by: 1, created_at: isoDate });

  expect((await api.post(`/internal/personas/${personaId}/prompt`, { data: { system_prompt: "  " } })).status()).toBe(400);
  expect((await api.post(`/internal/personas/999999999/prompt`, { data: { system_prompt: "x" } })).status()).toBe(404);
});

test("PUT /internal/personas/{id}/playbooks", async () => {
  await expectRejected("PUT", `/internal/personas/${personaId}/playbooks`, { scam_types: ["job"] });
  const res = await api.put(`/internal/personas/${personaId}/playbooks`, { data: { scam_types: ["job", "loan"] } });
  expect(res.status()).toBe(200);
  expect(await res.json()).toEqual({ id: personaId, scam_types: ["job", "loan"] });
  const list = (await (await api.get("/internal/personas")).json()) as Persona[];
  expect(list.find((p) => p.id === personaId)?.scam_types.sort()).toEqual(["job", "loan"]);

  expect((await api.put(`/internal/personas/${personaId}/playbooks`, { data: { scam_types: ["astrology"] } })).status()).toBe(400);
  expect((await api.put(`/internal/personas/999999999/playbooks`, { data: { scam_types: ["job"] } })).status()).toBe(404);
});

test("GET /internal/playbooks", async () => {
  await expectRejected("GET", "/internal/playbooks");
  const res = await api.get("/internal/playbooks");
  expect(res.status()).toBe(200);
  const list = (await res.json()) as { id: number; scam_type: string; name: string; version: number; stages: { key: string; name: string; goal: string; example: string }[] }[];
  expect(list.map((p) => p.scam_type).sort()).toEqual(expect.arrayContaining(["impersonation", "investment", "job", "loan", "parcel", "romance"]));
  for (const pb of list) {
    expect(pb.stages.map((s) => s.key)).toEqual(["rapport", "bait", "payment_request", "stall"]);
    for (const s of pb.stages) expect(s).toEqual({ key: expect.any(String), name: expect.any(String), goal: expect.any(String), example: expect.any(String) });
  }
});

// ───────────────────────── live.go: accounts ─────────────────────────

test("GET /internal/accounts", async () => {
  await expectRejected("GET", "/internal/accounts");
  const res = await api.get("/internal/accounts");
  expect(res.status()).toBe(200);
  const list = (await res.json()) as Account[];
  const sim = list.find((a) => a.id === simAccountId);
  expect(sim).toMatchObject({ persona_id: personaId, channel: "telegram", phone: `sim-${personaId}`, session_ref: expect.stringMatching(/^sim:/), status: "active" });
});

test("POST /internal/accounts", async () => {
  await expectRejected("POST", "/internal/accounts", { persona_id: personaId });
  const phone = `+6019${run.slice(-7)}`;
  const res = await api.post("/internal/accounts", { data: { persona_id: personaId, channel: "telegram", phone, session_ref: `e2e-${run}` } });
  expect(res.status()).toBe(201);
  const body = (await res.json()) as { account: Account; connected: boolean; connect_error?: string };
  expect(body.account).toMatchObject({ id: expect.any(Number), persona_id: personaId, channel: "telegram", phone, session_ref: `e2e-${run}`, status: "active" });
  // No Telegram session file exists for a made-up number, so the connector must say so.
  expect(body.connected).toBe(false);
  expect(body.connect_error).toEqual(expect.stringContaining("session"));
  // Park it so the API never retries a session that doesn't exist.
  expect((await api.post(`/internal/accounts/${body.account.id}/status`, { data: { status: "banned" } })).status()).toBe(200);

  expect((await api.post("/internal/accounts", { data: { persona_id: personaId, phone: "" } })).status()).toBe(400);
  expect((await api.post("/internal/accounts", { data: { persona_id: 999999999, phone, session_ref: "x" } })).status()).toBe(404);
});

test("POST /internal/accounts/{id}/connect", async () => {
  await expectRejected("POST", `/internal/accounts/${simAccountId}/connect`, {});
  // A simulated account needs no Telegram session: connect succeeds.
  const res = await api.post(`/internal/accounts/${simAccountId}/connect`, { data: {} });
  expect(res.status(), await res.text()).toBe(200);
  expect(await res.json()).toEqual({ id: simAccountId, connected: true });
  expect((await api.post(`/internal/accounts/999999999/connect`, { data: {} })).status()).toBe(404);
});

test("POST /internal/accounts/{id}/status", async () => {
  await expectRejected("POST", `/internal/accounts/${simAccountId}/status`, { status: "cooldown" });
  const until = new Date(Date.now() + 60_000).toISOString();
  const res = await api.post(`/internal/accounts/${simAccountId}/status`, { data: { status: "cooldown", cooldown_until: until } });
  expect(res.status()).toBe(200);
  expect(await res.json()).toEqual({ id: simAccountId, status: "cooldown" });
  const listed = ((await (await api.get("/internal/accounts")).json()) as (Account & { cooldown_until: string | null })[]).find((a) => a.id === simAccountId);
  expect(listed?.status).toBe("cooldown");
  expect(listed?.cooldown_until).toEqual(isoDate);
  expect((await api.post(`/internal/accounts/${simAccountId}/status`, { data: { status: "active" } })).status()).toBe(200);

  expect((await api.post(`/internal/accounts/${simAccountId}/status`, { data: { status: "lost" } })).status()).toBe(400);
  expect((await api.post(`/internal/accounts/999999999/status`, { data: { status: "active" } })).status(), "unknown account id").toBe(404);
});

// ───────────────────────── live.go: conversations ─────────────────────────

test("GET /internal/conversations", async () => {
  await expectRejected("GET", "/internal/conversations");
  const res = await api.get("/internal/conversations?limit=500");
  expect(res.status()).toBe(200);
  const c = ((await res.json()) as Conversation[]).find((x) => x.id === conversationId);
  expect(c).toMatchObject({
    account_id: simAccountId,
    scammer_handle: `@sim_${scammer}`,
    status: "bot",
    persona_id: personaId,
    channel: "telegram",
    persona_name: `E2E Fixture ${run}`,
    temporal_workflow_id: expect.stringMatching(/^conversation-/),
    started_at: isoDate,
  });
  expect(["rapport", "bait", "payment_request", "stall"]).toContain(c!.stage);

  const bots = (await (await api.get("/internal/conversations?status=bot&limit=500")).json()) as Conversation[];
  expect(bots.every((x) => x.status === "bot")).toBe(true);
  expect((await api.get("/internal/conversations?status=sleeping")).status()).toBe(400);
});

test("GET /internal/conversations/{id}", async () => {
  await expectRejected("GET", `/internal/conversations/${conversationId}`);
  const res = await api.get(`/internal/conversations/${conversationId}`);
  expect(res.status()).toBe(200);
  const d = (await res.json()) as ConversationDetail;
  expect(d.conversation).toMatchObject({ id: conversationId, scammer_handle: `@sim_${scammer}` });
  expect(d.messages[0]).toEqual({ id: expect.any(Number), direction: "inbound", sender_type: "scammer", text: expect.stringContaining(group4(account)), sent_at: isoDate });
  expect(Array.isArray(d.entities)).toBe(true);

  expect((await api.get(`/internal/conversations/0`)).status()).toBe(400);
  expect((await api.get(`/internal/conversations/999999999`)).status()).toBe(404);
});

test("POST /internal/conversations/{id}/takeover", async () => {
  await expectRejected("POST", `/internal/conversations/${conversationId}/takeover`, { mode: "human" });
  const res = await api.post(`/internal/conversations/${conversationId}/takeover`, { data: { mode: "human" } });
  expect(res.status()).toBe(200);
  expect(await res.json()).toEqual({ id: conversationId, status: "human" });
  // An empty body toggles back to the bot.
  const toggled = await api.post(`/internal/conversations/${conversationId}/takeover`, { data: {} });
  expect(await toggled.json()).toEqual({ id: conversationId, status: "bot" });
  expect((await api.post(`/internal/conversations/${conversationId}/takeover`, { data: { mode: "robot" } })).status()).toBe(400);
  expect((await api.post(`/internal/conversations/999999999/takeover`, { data: { mode: "human" } })).status()).toBe(404);
});

test("POST /internal/conversations/{id}/operator-message", async () => {
  await expectRejected("POST", `/internal/conversations/${conversationId}/operator-message`, { text: "x" });
  expect((await api.post(`/internal/conversations/${conversationId}/takeover`, { data: { mode: "human" } })).status()).toBe(200);
  const text = `operator e2e ${run}`;
  const res = await api.post(`/internal/conversations/${conversationId}/operator-message`, { data: { text } });
  expect(res.status(), await res.text()).toBe(201);
  expect(await res.json()).toMatchObject({ id: expect.any(Number), direction: "outbound", sender_type: "operator", operator_id: 1, text, sent_at: isoDate });
  expect((await api.post(`/internal/conversations/${conversationId}/operator-message`, { data: { text: "" } })).status()).toBe(400);

  // Back to the bot: the operator can no longer send (409).
  const back = await api.post(`/internal/conversations/${conversationId}/takeover`, { data: { mode: "bot" } });
  expect(await back.json()).toEqual({ id: conversationId, status: "bot" });
  expect((await api.post(`/internal/conversations/${conversationId}/operator-message`, { data: { text } })).status()).toBe(409);
});

// ───────────────────────── live.go: entities ─────────────────────────

test("GET /internal/entities", async () => {
  await expectRejected("GET", "/internal/entities");
  const e = await fixtureEntity();
  expect(e).toMatchObject({ id: expect.any(Number), status: expect.stringMatching(/^(unverified|verified)$/), sightings_count: expect.any(Number), first_seen: isoDate, last_seen: isoDate });
  expect(e.status === "verified" ? ["high"] : ["low", "medium"]).toContain(e.confidence);

  const unverified = (await (await api.get("/internal/entities?status=unverified&limit=500")).json()) as Entity[];
  expect(unverified.every((x) => x.status === "unverified")).toBe(true);
  expect((await api.get("/internal/entities?status=maybe")).status()).toBe(400);
});

test("POST /internal/entities/{id}/verify", async () => {
  const e = await fixtureEntity();
  await expectRejected("POST", `/internal/entities/${e.id}/verify`, { decision: "verified" });
  const res = await api.post(`/internal/entities/${e.id}/verify`, { data: { decision: "verified", note: `e2e ${run}` } });
  expect(res.status()).toBe(200);
  expect(await res.json()).toMatchObject({ id: e.id, type: "bank_account", value_normalized: account, status: "verified", confidence: "high" });

  // Once verified, the bank's lookup matches (policy A).
  const hit = (await (await api.post("/v1/lookup", { data: { type: "bank_account", value: account, bank: "MBB" } })).json()) as { sightings: number };
  expect(hit).toMatchObject({ match: true, status: "verified", confidence: "high", first_seen: isoDate, last_seen: isoDate });
  expect(hit.sightings).toBeGreaterThanOrEqual(1);

  expect((await api.post(`/internal/entities/${e.id}/verify`, { data: { decision: "perhaps" } })).status()).toBe(400);
  expect((await api.post(`/internal/entities/999999999/verify`, { data: { decision: "verified" } })).status()).toBe(404);
});

// ───────────────────────── live.go: status ─────────────────────────

test("GET /internal/status", async () => {
  await expectRejected("GET", "/internal/status");
  const res = await api.get("/internal/status");
  expect(res.status()).toBe(200);
  const s = (await res.json()) as { telegram: { connected: boolean; last_message_at: string | null }; llm: { errors_recent: number }; workflows: { running: number | null; error?: string } };
  expect(typeof s.telegram.connected).toBe("boolean");
  expect(s.telegram).toHaveProperty("last_message_at");
  expect(typeof s.llm.errors_recent).toBe("number");
  expect(s.workflows.error).toBeUndefined();
  expect(s.workflows.running).toEqual(expect.any(Number));
});

// ───────────────────────── live.go: web chat simulator ─────────────────────────

test("POST /internal/sim/accounts", async () => {
  await expectRejected("POST", "/internal/sim/accounts", { persona_id: personaId });
  const res = await api.post("/internal/sim/accounts", { data: { persona_id: personaId } });
  expect(res.status()).toBe(200);
  expect(await res.json()).toEqual({ account_id: simAccountId, persona: `E2E Fixture ${run}`, persona_status: "active" }); // idempotent
  expect((await api.post("/internal/sim/accounts", { data: { persona_id: 999999999 } })).status()).toBe(404);
});

test("POST /internal/sim/messages", async () => {
  await expectRejected("POST", "/internal/sim/messages", { account_id: simAccountId, from: scammer, text: "hi" });
  const res = await api.post("/internal/sim/messages", { data: { account_id: simAccountId, from: scammer, text: `still there? ref ${run}` } });
  expect(res.status()).toBe(202);
  expect(await res.json()).toEqual({ ok: true });
  await pollFor<ConversationDetail, boolean>(api, `/internal/conversations/${conversationId}`, (d) => d.messages.some((m) => m.text === `still there? ref ${run}`));

  expect((await api.post("/internal/sim/messages", { data: { account_id: simAccountId, from: scammer, text: "" } })).status()).toBe(400);
  expect((await api.post("/internal/sim/messages", { data: { account_id: 999999999, from: scammer, text: "x" } })).status()).toBe(404);
});

test("GET /internal/sim/events", async () => {
  test.setTimeout(120_000);
  await expectRejected("GET", `/internal/sim/events?account_id=${simAccountId}&from=${scammer}`);
  expect((await api.get("/internal/sim/events")).status()).toBe(400);
  // The bot answers the fixture chat over the simulator: wait for a persona message event.
  const deadline = Date.now() + 90_000;
  let after = 0;
  let got: { seq: number; kind: string; text?: string; at: string } | undefined;
  while (!got && Date.now() < deadline) {
    const res = await api.get(`/internal/sim/events?account_id=${simAccountId}&from=${scammer}&after=${after}`, { timeout: 30_000 });
    expect(res.status()).toBe(200);
    const evs = (await res.json()) as { seq: number; kind: string; text?: string; at: string }[];
    expect(Array.isArray(evs)).toBe(true);
    for (const e of evs) {
      expect(e).toMatchObject({ seq: expect.any(Number), kind: expect.stringMatching(/^(message|typing|read)$/), at: isoDate });
      after = Math.max(after, e.seq);
    }
    got = evs.find((e) => e.kind === "message" && !!e.text);
  }
  expect(got, "a persona message event on the simulator within 90 s").toBeTruthy();
});

test("POST /internal/conversations/{id}/close", async () => {
  // Its own chat, so the shared fixture stays open for the other tests.
  const other = freshScammer("close");
  expect((await api.post("/internal/sim/messages", { data: { account_id: simAccountId, from: other, text: "hello, who is this?" } })).status()).toBe(202);
  const id = await pollFor<Conversation[], number>(api, "/internal/conversations?limit=500", (l) => l.find((c) => c.scammer_handle === `@sim_${other}`)?.id);
  await expectRejected("POST", `/internal/conversations/${id}/close`, {});
  const res = await api.post(`/internal/conversations/${id}/close`, { data: {} });
  expect(res.status()).toBe(200);
  expect(await res.json()).toEqual({ id, status: "closed" });
  const d = (await (await api.get(`/internal/conversations/${id}`)).json()) as ConversationDetail;
  expect(d.conversation.status).toBe("closed");
  expect((await api.post(`/internal/conversations/999999999/close`, { data: {} })).status()).toBe(404);
});

// ───────────────────────── live.go: scam phase history, AI summaries ─────────────────────────

interface SummaryRow {
  id: number;
  summary: string;
  model: string;
  created_at: string;
}

test("GET /internal/conversations/{id}/stages", async () => {
  await expectRejected("GET", `/internal/conversations/${conversationId}/stages`);
  const res = await api.get(`/internal/conversations/${conversationId}/stages`);
  expect(res.status()).toBe(200);
  const h = (await res.json()) as { conversation_id: number; stage: string; events: { from_stage: string | null; to_stage: string; source: string; created_at: string }[] };
  expect(h).toMatchObject({ conversation_id: conversationId, stage: expect.any(String) });
  // The first row is the conversation's start (from_stage null, rapport, written by the bot).
  expect(h.events[0]).toMatchObject({ from_stage: null, to_stage: "rapport", source: "bot", created_at: isoDate });
  for (const e of h.events) expect(["bot", "operator", "classifier"]).toContain(e.source);
  expect(h.events.at(-1)!.to_stage, "the newest event ends at the current stage").toBe(h.stage);
  expect((await api.get(`/internal/conversations/0/stages`)).status()).toBe(400);
  expect((await api.get(`/internal/conversations/999999999/stages`)).status()).toBe(404);
});

test("GET + POST /internal/conversations/{id}/summary", async () => {
  test.setTimeout(240_000);
  await expectRejected("GET", `/internal/conversations/${conversationId}/summary`);
  await expectRejected("POST", `/internal/conversations/${conversationId}/summary`, {});
  const get = await api.get(`/internal/conversations/${conversationId}/summary`);
  expect(get.status()).toBe(200);
  expect(await get.json()).toMatchObject({ conversation_id: conversationId, message_count: expect.any(Number), stale: expect.any(Boolean) });

  const post = await api.post(`/internal/conversations/${conversationId}/summary`, { data: {}, timeout: 200_000 });
  const text = await post.text();
  expect(post.status(), `POST summary → ${post.status()} ${text.slice(0, 300)}`).toBe(201);
  const made = JSON.parse(text) as { summary: SummaryRow & { conversation_id: number; stage: string; message_count_at: number; reason: string } };
  expect(made.summary).toMatchObject({ conversation_id: conversationId, model: expect.any(String), reason: "on_demand", created_at: isoDate });
  expect(made.summary.summary.length, "a written summary, not a template").toBeGreaterThan(60);
  expect(made.summary.message_count_at).toBeGreaterThan(0);

  const again = (await (await api.get(`/internal/conversations/${conversationId}/summary`)).json()) as { summary: SummaryRow };
  expect(again.summary.id, "GET returns the newest stored summary").toBe(made.summary.id);
  expect((await api.get(`/internal/conversations/999999999/summary`)).status()).toBe(404);
  expect((await api.post(`/internal/conversations/999999999/summary`, { data: {} })).status()).toBe(404);
});

test("GET + POST /internal/personas/{id}/summary", async () => {
  test.setTimeout(240_000);
  await expectRejected("GET", `/internal/personas/${personaId}/summary`);
  await expectRejected("POST", `/internal/personas/${personaId}/summary`, {});
  const get = await api.get(`/internal/personas/${personaId}/summary`);
  expect(get.status()).toBe(200);
  expect(await get.json()).toMatchObject({ persona_id: personaId, conversation_count: expect.any(Number) });
  const post = await api.post(`/internal/personas/${personaId}/summary`, { data: {}, timeout: 200_000 });
  const text = await post.text();
  expect(post.status(), `POST persona summary → ${post.status()} ${text.slice(0, 300)}`).toBe(201);
  const made = JSON.parse(text) as { summary: SummaryRow & { persona_id: number } };
  expect(made.summary).toMatchObject({ persona_id: personaId, model: expect.any(String), created_at: isoDate });
  expect(made.summary.summary.length).toBeGreaterThan(60);
  const got = (await (await api.get(`/internal/personas/${personaId}/summary`)).json()) as { summary: SummaryRow; stale: boolean };
  expect(got.summary.id).toBe(made.summary.id);
  expect(got.stale).toBe(false);
  expect((await api.get(`/internal/personas/999999999/summary`)).status()).toBe(404);
});

test("GET + POST /internal/summary (dashboard overview)", async () => {
  test.setTimeout(240_000);
  await expectRejected("GET", "/internal/summary");
  await expectRejected("POST", "/internal/summary", {});
  const get = await api.get("/internal/summary");
  expect(get.status()).toBe(200);
  const before = (await get.json()) as { active_personas: number; active_conversations: number; stale: boolean };
  expect(before.active_personas, "the fixture persona is active").toBeGreaterThan(0);
  expect(before.active_conversations, "the fixture conversation is open").toBeGreaterThan(0);
  const post = await api.post("/internal/summary", { data: {}, timeout: 200_000 });
  const text = await post.text();
  expect(post.status(), `POST /internal/summary → ${post.status()} ${text.slice(0, 300)}`).toBe(201);
  const made = JSON.parse(text) as { summary: SummaryRow & { active_personas: number; active_conversations: number } };
  expect(made.summary).toMatchObject({ model: expect.any(String), active_personas: expect.any(Number), active_conversations: expect.any(Number), created_at: isoDate });
  expect(made.summary.summary.length).toBeGreaterThan(60);
  const after = (await (await api.get("/internal/summary")).json()) as { summary: SummaryRow };
  expect(after.summary.id).toBeGreaterThanOrEqual(made.summary.id);
});

// ───────────────────────── live.go: customers, API keys, usage (FR-13) ─────────────────────────

test("customers + API keys: create, issue (shown once), list, lookup with the key, usage, revoke", async () => {
  await expectRejected("GET", "/internal/customers");
  await expectRejected("POST", "/internal/customers", { name: "x", type: "bank" });

  const created = await api.post("/internal/customers", { data: { name: `E2E Bank ${run}`, type: "bank" } });
  expect(created.status(), await created.text()).toBe(201);
  const cust = (await created.json()) as { id: number; name: string; type: string };
  expect(cust).toMatchObject({ id: expect.any(Number), name: `E2E Bank ${run}`, type: "bank" });
  expect((await api.post("/internal/customers", { data: { name: "", type: "bank" } })).status()).toBe(400);
  expect((await api.post("/internal/customers", { data: { name: "x", type: "casino" } })).status()).toBe(400);
  const listed = (await (await api.get("/internal/customers")).json()) as { id: number }[];
  expect(listed.map((c) => c.id)).toContain(cust.id);

  await expectRejected("POST", `/internal/customers/${cust.id}/keys`, {});
  const issued = await api.post(`/internal/customers/${cust.id}/keys`, { data: { rate_limit_per_min: 120 } });
  expect(issued.status(), await issued.text()).toBe(201);
  const k = (await issued.json()) as { key: string; api_key: { id: number; key_prefix: string; status: string; rate_limit_per_min: number } & Record<string, unknown> };
  expect(k.key).toMatch(/^sk_live_[0-9a-f]{48}$/);
  expect(k.api_key).toMatchObject({ key_prefix: k.key.slice(0, 12), status: "active", rate_limit_per_min: 120 });
  expect(k.api_key, "the hash is never returned").not.toHaveProperty("key_hash");
  expect((await api.post(`/internal/customers/999999999/keys`, { data: {} })).status()).toBe(404);

  await expectRejected("GET", `/internal/customers/${cust.id}/keys`);
  const keys = (await (await api.get(`/internal/customers/${cust.id}/keys`)).json()) as Record<string, unknown>[];
  expect(keys).toHaveLength(1);
  expect(JSON.stringify(keys), "the raw key is shown once only").not.toContain(k.key);

  // POST /v1/lookup with the customer key (not the operator token): answered and logged.
  const bank = await request.newContext({ baseURL: API_URL, extraHTTPHeaders: { Authorization: `Bearer ${k.key}` } });
  try {
    const miss = await bank.post("/v1/lookup", { data: { type: "bank_account", value: freshAccountNumber(), bank: "MBB" } });
    expect(miss.status()).toBe(200);
    expect(await miss.json()).toEqual({ match: false });
    const hit = await bank.post("/v1/lookup", { data: { type: "bank_account", value: account, bank: "mbb" } });
    expect(hit.status()).toBe(200);

    await expectRejected("GET", `/internal/customers/${cust.id}/usage`);
    const usage = (await (await api.get(`/internal/customers/${cust.id}/usage`)).json()) as {
      months: { lookups: number; matches: number }[];
      recent: { customer_id: number; api_key_id: number; entity_type: string; matched: boolean; latency_ms: number; ts: string }[];
    };
    expect(usage.recent, "one lookup_logs row per keyed lookup").toHaveLength(2);
    for (const r of usage.recent) expect(r).toMatchObject({ customer_id: cust.id, api_key_id: k.api_key.id, entity_type: "bank_account", latency_ms: expect.any(Number), ts: isoDate });
    expect(JSON.stringify(usage), "lookup_logs never stores the looked-up value (FR-13)").not.toContain(account);
    expect(usage.months[0].lookups).toBe(2);
    const counted = (await (await api.get(`/internal/customers/${cust.id}/keys`)).json()) as { lookups: number }[];
    expect(counted[0].lookups).toBe(2);

    // Lookups with the operator token still work and are not logged against the customer.
    expect((await api.post("/v1/lookup", { data: { type: "bank_account", value: freshAccountNumber() } })).status()).toBe(200);

    await expectRejected("DELETE", `/internal/customers/${cust.id}/keys/${k.api_key.id}`);
    const rev = await api.delete(`/internal/customers/${cust.id}/keys/${k.api_key.id}`);
    expect(rev.status()).toBe(200);
    expect(await rev.json()).toMatchObject({ id: k.api_key.id, status: "revoked", revoked_at: isoDate });
    expect((await api.delete(`/internal/customers/${cust.id}/keys/${k.api_key.id}`)).status(), "already revoked").toBe(404);
    const after = await bank.post("/v1/lookup", { data: { type: "bank_account", value: account } });
    expect(after.status(), "a revoked key is refused").toBe(401);
    expect(((await (await api.get(`/internal/customers/${cust.id}/usage`)).json()) as { recent: unknown[] }).recent).toHaveLength(2);
  } finally {
    await bank.dispose();
  }
});

test("GET /internal/accounts/{id}/health", async () => {
  await expectRejected("GET", `/internal/accounts/${simAccountId}/health`);
  // A status change by the operator is recorded as a health event (G-10).
  expect((await api.post(`/internal/accounts/${simAccountId}/status`, { data: { status: "cooldown", cooldown_until: new Date(Date.now() + 60_000).toISOString() } })).status()).toBe(200);
  expect((await api.post(`/internal/accounts/${simAccountId}/status`, { data: { status: "active" } })).status()).toBe(200);
  const res = await api.get(`/internal/accounts/${simAccountId}/health`);
  expect(res.status()).toBe(200);
  const evs = (await res.json()) as { account_id: number; event: string; detail: { from: string; to: string; source: string }; created_at: string }[];
  expect(evs[0]).toMatchObject({ account_id: simAccountId, event: "active", detail: { from: "cooldown", to: "active", source: "operator" }, created_at: isoDate });
  expect(evs[1]).toMatchObject({ event: "cooldown", detail: { to: "cooldown" } });
  expect((await api.get(`/internal/accounts/999999999/health`)).status()).toBe(404);
});

// ───────────────────────── web/src/app/api/**/route.ts ─────────────────────────

// A console context signed in through the real sign-in route.
async function consoleSession(): Promise<APIRequestContext> {
  const ctx = await request.newContext({ baseURL: WEB_URL });
  const user = process.env.SOCKABUS_CONSOLE_USER;
  const password = process.env.SOCKABUS_CONSOLE_PASSWORD;
  expect(user && password, "set SOCKABUS_CONSOLE_USER / SOCKABUS_CONSOLE_PASSWORD (console sign-in)").toBeTruthy();
  const res = await ctx.post("/api/auth/sign-in", { form: { user: user!, password: password! }, maxRedirects: 0 });
  expect(res.status()).toBe(303);
  return ctx;
}

test("web POST /api/auth/sign-in", async () => {
  const ctx = await request.newContext({ baseURL: WEB_URL });
  const wrongPw = await ctx.post("/api/auth/sign-in", { form: { user: process.env.SOCKABUS_CONSOLE_USER ?? "", password: "definitely-wrong", next: "/verify" }, maxRedirects: 0 });
  expect(wrongPw.status()).toBe(303);
  expect(wrongPw.headers()["location"]).toBe("/sign-in?error=wrong&next=%2Fverify");
  expect(wrongPw.headers()["set-cookie"] ?? "").not.toContain("sockabus_session=");

  const ok = await ctx.post("/api/auth/sign-in", {
    form: { user: process.env.SOCKABUS_CONSOLE_USER ?? "", password: process.env.SOCKABUS_CONSOLE_PASSWORD ?? "", next: "/verify" },
    maxRedirects: 0,
  });
  expect(ok.status()).toBe(303);
  expect(ok.headers()["location"]).toBe("/verify");
  expect(ok.headers()["set-cookie"]).toMatch(/sockabus_session=[^;]+;.*HttpOnly/i);
  await ctx.dispose();
});

test("web GET /api/auth/me", async () => {
  const anonWeb = await request.newContext({ baseURL: WEB_URL });
  const no = await anonWeb.get("/api/auth/me");
  expect(no.status()).toBe(401);
  expect(await no.json()).toEqual({ error: expect.any(String), sign_in: "/sign-in" });
  await anonWeb.dispose();

  const ctx = await consoleSession();
  const res = await ctx.get("/api/auth/me");
  expect(res.status()).toBe(200);
  expect(await res.json()).toEqual({ user: process.env.SOCKABUS_CONSOLE_USER, gate: true });
  await ctx.dispose();
});

test("web POST /api/auth/sign-out", async () => {
  const anonWeb = await request.newContext({ baseURL: WEB_URL });
  expect((await anonWeb.post("/api/auth/sign-out")).status()).toBe(401);
  await anonWeb.dispose();

  const ctx = await consoleSession();
  const res = await ctx.post("/api/auth/sign-out");
  expect(res.status()).toBe(200);
  expect(await res.json()).toEqual({ ok: true });
  expect(res.headers()["set-cookie"]).toMatch(/sockabus_session=;.*Max-Age=0/i);
  expect((await ctx.get("/api/auth/me")).status(), "signed out: /api/auth/me is gated again").toBe(401);
  await ctx.dispose();
});

test("web POST /api/demo/lookup", async () => {
  const anonWeb = await request.newContext({ baseURL: WEB_URL });
  expect((await anonWeb.post("/api/demo/lookup", { data: { bank: "MBB", account } })).status()).toBe(401);
  await anonWeb.dispose();

  const ctx = await consoleSession();
  const empty = await ctx.post("/api/demo/lookup", { data: { bank: "MBB" } });
  expect(empty.status()).toBe(400);
  expect(await empty.json()).toEqual({ error: "account is required" });

  // Verify the fixture account, then the bank route reports a high-confidence hit.
  const e = await fixtureEntity();
  expect((await api.post(`/internal/entities/${e.id}/verify`, { data: { decision: "verified", note: `e2e web ${run}` } })).status()).toBe(200);
  const hit = await ctx.post("/api/demo/lookup", { data: { bank: "MBB", account: group4(account) } });
  expect(hit.status()).toBe(200);
  const h = (await hit.json()) as { res: { match: boolean; confidence: string; sightings: number }; ms: number };
  expect(h.res).toMatchObject({ match: true, confidence: "high" });
  expect(h.ms).toEqual(expect.any(Number));

  const miss = await ctx.post("/api/demo/lookup", { data: { bank: "MBB", account: freshAccountNumber() } });
  expect(miss.status()).toBe(200);
  expect(((await miss.json()) as { res: unknown }).res).toEqual({ match: false });
  await ctx.dispose();
});

test("web /api/[...path] proxy", async () => {
  const anonWeb = await request.newContext({ baseURL: WEB_URL });
  expect((await anonWeb.get("/api/internal/status")).status()).toBe(401);
  await anonWeb.dispose();

  const ctx = await consoleSession();
  // GET is forwarded to the Go API with the operator token added server-side.
  const st = await ctx.get("/api/internal/status");
  expect(st.status()).toBe(200);
  expect(await st.json()).toMatchObject({ telegram: { connected: expect.any(Boolean) }, llm: { errors_recent: expect.any(Number) } });
  // Query strings pass through.
  const convs = await ctx.get(`/api/internal/conversations?limit=500&status=bot`);
  expect(convs.status()).toBe(200);
  const bots = (await convs.json()) as Conversation[];
  expect(bots.every((c) => c.status === "bot")).toBe(true);
  expect(bots.some((c) => c.id === conversationId)).toBe(true);
  // POST bodies pass through, and Go errors keep their status.
  const bad = await ctx.post("/api/internal/personas", { data: { name: "" } });
  expect(bad.status()).toBe(400);
  expect(await bad.json()).toEqual({ error: expect.any(String) });
  // No open relay to the customer API.
  const v1 = await ctx.post("/api/v1/lookup", { data: { type: "bank_account", value: account } });
  expect(v1.status()).toBe(404);
  expect(await v1.json()).toEqual({ error: "not found" });
  await ctx.dispose();
});
