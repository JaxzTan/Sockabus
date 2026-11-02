// v1.0 demo path through the operator API (PRD §10, TRD §9). Needs a live-mode backend with
// SOCKABUS_SIM=1 and a 1 s reply delay (see playwright/README.md).
// Every run uses a new persona, scammer and account number, and retires/closes them at the end,
// so reruns pass on the same database (messages and verifications are append-only by design).
// One test with a step per stage: a failing stage fails the test and is named in the report,
// instead of the later stages being reported as "did not run".
import { expect, test, type APIRequestContext } from "@playwright/test";
import { type Conversation, type ConversationDetail, freshAccountNumber, generateActivePersona, group4, operatorContext, pollFor } from "../helpers";

let api: APIRequestContext;
const run = `${Date.now()}`;
const scammer = `e2e_${run}`;
const account = freshAccountNumber();
let personaId = 0;
let conversationId = 0;

test.beforeAll(async () => {
  api = await operatorContext();
});

test.afterAll(async () => {
  if (conversationId) await api.post(`/internal/conversations/${conversationId}/close`, { data: {} });
  if (personaId) await api.post(`/internal/personas/${personaId}/status`, { data: { status: "retired" } });
  await api.dispose();
});

test("operator routes reject a missing token", async ({ request }) => {
  for (const path of ["/internal/status", "/internal/personas", "/internal/entities"]) {
    expect((await request.get(path)).status(), path).toBe(401);
  }
  expect((await request.post("/v1/lookup", { data: { type: "bank_account", value: account } })).status()).toBe(401);
});

test("demo path: persona → scammer → reply → takeover → entity → verify → lookup", async () => {
  test.setTimeout(420_000);
  let accountId = 0;
  let entityId = 0;

  await test.step("1. generate a persona with one click", async () => {
    personaId = await generateActivePersona(api);
  });

  await test.step("2. scammer message gets a persona reply", async () => {
    const acct = await api.post("/internal/sim/accounts", { data: { persona_id: personaId } });
    expect(acct.ok()).toBeTruthy();
    accountId = ((await acct.json()) as { account_id: number }).account_id;

    const sent = await api.post("/internal/sim/messages", {
      data: { account_id: accountId, from: scammer, text: `Hi auntie, part time job RM300 a day. Transfer RM50 deposit to Maybank ${group4(account)} now` },
    });
    expect(sent.status()).toBe(202);

    conversationId = await pollFor<Conversation[], number>(
      api,
      "/internal/conversations?limit=50",
      (list) => list.find((c) => c.scammer_handle === `@sim_${scammer}` && c.persona_id === personaId)?.id,
    );
    const reply = await pollFor<ConversationDetail, string>(
      api,
      `/internal/conversations/${conversationId}`,
      (d) => d.messages.find((m) => m.sender_type === "persona")?.text,
      { timeoutMs: 90_000 },
    );
    expect(reply.length).toBeGreaterThan(0);
  });

  await test.step("3. operator takes over and sends a message", async () => {
    const take = await api.post(`/internal/conversations/${conversationId}/takeover`, { data: { mode: "human" } });
    expect(take.ok()).toBeTruthy();
    expect(((await take.json()) as { status: string }).status).toBe("human");

    const text = `operator check ${run}`;
    const msg = await api.post(`/internal/conversations/${conversationId}/operator-message`, { data: { text } });
    expect(msg.status()).toBe(201);
    await pollFor<ConversationDetail, boolean>(api, `/internal/conversations/${conversationId}`, (d) =>
      d.messages.some((m) => m.sender_type === "operator" && m.text === text),
    );
  });

  await test.step("4. the bank account appears as an extracted entity", async () => {
    const entity = await pollFor<ConversationDetail, ConversationDetail["entities"][number]>(
      api,
      `/internal/conversations/${conversationId}`,
      (d) => d.entities.find((e) => e.type === "bank_account" && e.value_normalized === account),
    );
    expect(entity.status).toBe("unverified");
    expect(entity.confidence).not.toBe("high"); // FR-9: only an operator sets high
    entityId = entity.id;
  });

  await test.step("5. operator verifies the entity", async () => {
    const res = await api.post(`/internal/entities/${entityId}/verify`, { data: { decision: "verified", note: `e2e ${run}` } });
    expect(res.ok()).toBeTruthy();
    const e = (await res.json()) as { status: string; confidence: string };
    expect(e.status).toBe("verified");
    expect(e.confidence).toBe("high");
  });

  await test.step("6. bank lookup returns a hit", async () => {
    const res = await api.post("/v1/lookup", { data: { type: "bank_account", value: account, bank: "MBB" } });
    expect(res.ok()).toBeTruthy();
    const hit = (await res.json()) as { match: boolean; confidence: string; sightings: number };
    expect(hit.match).toBe(true);
    expect(hit.confidence).toBe("high");
    expect(hit.sightings).toBeGreaterThanOrEqual(1);

    const miss = await api.post("/v1/lookup", { data: { type: "bank_account", value: freshAccountNumber(), bank: "MBB" } });
    expect(((await miss.json()) as { match: boolean }).match).toBe(false);
  });
});

test("status reports the backend", async () => {
  const res = await api.get("/internal/status");
  expect(res.ok()).toBeTruthy();
  const s = (await res.json()) as { telegram: { connected: boolean }; llm: { errors_recent: number }; workflows: { running: number | null } };
  expect(typeof s.telegram.connected).toBe("boolean");
  expect(s.llm.errors_recent, "failed LLM calls in the last 15 min").toBe(0);
  expect(s.workflows.running).not.toBeNull();
});
