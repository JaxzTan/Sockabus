// End-to-end journeys through the operator API, one test per journey with a step per stage.
// Real backend, real database, real persona LLM (whatever the test stack runs: see playwright/README.md).
// Inputs (scammer names, messages, account numbers) are generated per run; nothing is mocked.
import { expect, test, type APIRequestContext } from "@playwright/test";
import {
  type Account,
  cannedLines,
  type Conversation,
  type ConversationDetail,
  createActivePersona,
  type Entity,
  freshAccountNumber,
  freshScammer,
  group4,
  llmErrors,
  operatorContext,
  type Persona,
  pollFor,
  retirePersona,
  signIn,
  simAccountFor,
  WEB_URL,
  simSend,
  waitForPersonaReplies,
} from "../helpers";

let api: APIRequestContext;
const cleanup: number[] = []; // persona ids to retire

test.beforeAll(async () => {
  api = await operatorContext();
});

test.afterAll(async () => {
  for (const id of cleanup) await retirePersona(api, id);
  await api.dispose();
});

const stageOf = async (convId: number) => ((await (await api.get(`/internal/conversations/${convId}`)).json()) as ConversationDetail).conversation.stage;

// @live: needs the real LLM (CI runs the fake one). CI excludes @live; local runs and the monitor do not.
test("a. generate personas: one click creates a full persona with real fields", { tag: "@live" }, async () => {
  test.setTimeout(360_000);
  let id = 0;
  let generated: { id: number; name: string; age: number; status: string; summary: string };

  await test.step("POST /internal/personas/generate with the real LLM", async () => {
    const res = await api.post("/internal/personas/generate", { data: { count: 1, mode: "random" }, timeout: 330_000 });
    const text = await res.text();
    expect(res.status(), `generate → ${res.status()}: ${text.slice(0, 400)}`).toBe(201);
    const body = JSON.parse(text) as { personas: (typeof generated)[] };
    expect(body.personas).toHaveLength(1);
    generated = body.personas[0];
    id = generated.id;
    cleanup.push(id);
    expect(generated.status).toBe("draft");
    expect(generated.age).toBeGreaterThanOrEqual(18);
    expect(generated.summary).toContain(String(generated.age));
  });

  await test.step("the stored persona carries every generated field", async () => {
    const list = (await (await api.get("/internal/personas")).json()) as Persona[];
    const p = list.find((x) => x.id === id);
    expect(p, `persona ${id} listed`).toBeTruthy();
    expect(p!.name).toBe(generated.name);
    expect(p!.age).toBe(generated.age);
    const llm = process.env.SOCKABUS_E2E_LLM ?? "unknown";
    expect(p!.backstory?.length ?? 0, `backstory is a written paragraph (stack LLM: ${llm}); got ${JSON.stringify(p!.backstory)}`).toBeGreaterThan(80);
    const mix = Object.values(p!.language_mix ?? {});
    expect(mix.length, "language_mix has languages").toBeGreaterThan(0);
    expect(mix.reduce((a, b) => a + b, 0)).toBeCloseTo(1, 1);
    expect(p!.wealth_profile, `wealth_profile has savings and income bracket (stack LLM: ${llm}); got ${JSON.stringify(p!.wealth_profile)}`).toMatchObject({ savings_rm: expect.any(Number), income_bracket: expect.any(String) });
    expect(p!.profile, "profile").toMatchObject({ city: expect.any(String), state: expect.any(String), gender: expect.any(String) });
    expect(p!.prompt_version, "system prompt stored as version 1").toBe(1);
  });

  await test.step("two generated personas are not duplicates (dedup memory)", async () => {
    const res = await api.post("/internal/personas/generate", { data: { count: 1, mode: "random" }, timeout: 330_000 });
    const text = await res.text();
    expect(res.status(), `second generate → ${res.status()}: ${text.slice(0, 400)}`).toBe(201);
    const second = (JSON.parse(text) as { personas: (typeof generated)[] }).personas[0];
    cleanup.push(second.id);
    expect(second.id).not.toBe(id);
    expect(second.name).not.toBe(generated.name);
  });

  await test.step("activate the persona", async () => {
    const res = await api.post(`/internal/personas/${id}/status`, { data: { status: "active" } });
    expect(await res.json()).toEqual({ id, status: "active" });
  });
});

// @live: needs a logged-in Telegram number (SMS login, impossible in CI).
test("b. Telegram backend: list and connect accounts, read a Telegram chat", { tag: "@live" }, async () => {
  let tg: Account | undefined;

  await test.step("GET /internal/accounts lists a real (non-simulated) Telegram account", async () => {
    const res = await api.get("/internal/accounts");
    expect(res.status()).toBe(200);
    const accounts = (await res.json()) as Account[];
    tg = accounts.find((a) => a.channel === "telegram" && !a.session_ref.startsWith("sim:") && a.status === "active");
    expect(
      tg,
      `No Telegram session exists in the test stack: honeypot_accounts has ${accounts.length} account(s), none an active, ` +
        `non-simulated Telegram account. Log a test number in against the e2e DB (go run ./cmd/tglogin with ` +
        `SOCKABUS_POSTGRES_URL set to the scratch DB and SOCKABUS_TG_SESSION_DIR=playwright/.e2e/telegram), then rerun.`,
    ).toBeTruthy();
  });

  await test.step("POST /internal/accounts/{id}/connect starts its Telegram session", async () => {
    const res = await api.post(`/internal/accounts/${tg!.id}/connect`, { data: {} });
    expect(res.status(), await res.text()).toBe(200);
    expect(await res.json()).toEqual({ id: tg!.id, connected: true });
    await expect
      .poll(async () => ((await (await api.get("/internal/status")).json()) as { telegram: { connected: boolean } }).telegram.connected, {
        message: "GET /internal/status reports Telegram connected",
        timeout: 60_000,
      })
      .toBe(true);
  });

  await test.step("GET /internal/conversations/{id} for a Telegram-channel chat", async () => {
    const list = (await (await api.get("/internal/conversations?limit=500")).json()) as Conversation[];
    const c = list.find((x) => x.account_id === tg!.id && x.channel === "telegram");
    expect(c, `no conversation on Telegram account ${tg!.id} yet: message the honeypot number from another Telegram account`).toBeTruthy();
    const d = (await (await api.get(`/internal/conversations/${c!.id}`)).json()) as ConversationDetail;
    expect(d.conversation).toMatchObject({ id: c!.id, account_id: tg!.id });
    expect(d.messages.length).toBeGreaterThan(0);
    expect(d.messages[0]).toMatchObject({ direction: "inbound", sender_type: "scammer", text: expect.any(String) });
  });
});

test("c. scam flow: web chat → persona replies → stages → takeover → extraction → verify → bank hit", async () => {
  test.setTimeout(420_000);
  const scammer = freshScammer("flow");
  const account = freshAccountNumber();
  const canned = await cannedLines(api);
  let persona: Persona;
  let accountId = 0;
  let convId = 0;
  let replies = 0;
  let entity: Entity;

  // The persona's reply must be model output, not the canned fallback the backend uses when the LLM
  // fails: no new LLM errors, and not the stage's example line. Soft, so later stages still run.
  const errorsBefore = await llmErrors(api);
  const checkReal = async (texts: string[], stage: string) => {
    for (const t of texts) {
      expect(t.trim().length, `${stage}: empty persona reply`).toBeGreaterThan(0);
      expect.soft(canned.has(t.trim()), `${stage}: persona replied with the canned fallback line "${t}" (persona LLM failed)`).toBe(false);
    }
    expect.soft(await llmErrors(api), `${stage}: LLM calls failed during this stage (GET /internal/status llm.errors_recent; see the API log)`).toBe(errorsBefore);
  };

  await test.step("set up an active persona with the job playbook and its web chat account", async () => {
    persona = await createActivePersona(api, "Flow");
    cleanup.push(persona.id);
    accountId = await simAccountFor(api, persona.id);
  });

  await test.step("stage rapport: scammer opens, persona replies", async () => {
    await simSend(api, accountId, scammer, "Hello, good afternoon. Is this the person who applied for a side income opportunity?");
    convId = await pollFor<Conversation[], number>(api, "/internal/conversations?limit=500", (l) => l.find((c) => c.scammer_handle === `@sim_${scammer}`)?.id, {
      what: "the new conversation",
    });
    const r = await waitForPersonaReplies(api, convId, replies);
    replies += r.length;
    await checkReal(r.map((m) => m.text), "rapport");
    expect(await stageOf(convId)).toBe("rapport");
  });

  await test.step("stage bait: the offer", async () => {
    await simSend(api, accountId, scammer, "We are hiring part time. You just like and review products on Shopee from your phone, earn RM300 a day. Interested?");
    const r = await waitForPersonaReplies(api, convId, replies);
    replies += r.length;
    await checkReal(r.map((m) => m.text), "bait");
    expect(await stageOf(convId)).toBe("bait");
  });

  await test.step("stage payment_request: scammer asks for a deposit", async () => {
    await simSend(api, accountId, scammer, "To activate your task account you must transfer a RM50 deposit first. It is refunded with your first commission.");
    const r = await waitForPersonaReplies(api, convId, replies);
    replies += r.length;
    await checkReal(r.map((m) => m.text), "payment_request");
    expect(await stageOf(convId)).toBe("payment_request");
  });

  await test.step("operator takes over and replies as the persona", async () => {
    const take = await api.post(`/internal/conversations/${convId}/takeover`, { data: { mode: "human" } });
    expect(await take.json()).toEqual({ id: convId, status: "human" });
    const text = `ok which account ah? ref ${scammer.slice(-6)}`;
    const msg = await api.post(`/internal/conversations/${convId}/operator-message`, { data: { text } });
    expect(msg.status()).toBe(201);
    const d = (await (await api.get(`/internal/conversations/${convId}`)).json()) as ConversationDetail;
    expect(d.conversation.status).toBe("human");
    expect(d.messages.at(-1)).toMatchObject({ direction: "outbound", sender_type: "operator", text });
  });

  await test.step("scammer sends the bank account: it is extracted while the operator holds the chat", async () => {
    await simSend(api, accountId, scammer, `Bank in RM50 to Maybank ${group4(account)}, name Tan Ah Kow. Send me the receipt after.`);
    entity = await pollFor<ConversationDetail, Entity>(
      api,
      `/internal/conversations/${convId}`,
      (d) => d.entities.find((e) => e.type === "bank_account" && e.value_normalized === account),
      { timeoutMs: 90_000, what: `bank account ${account} extracted` },
    );
    expect(entity.status).toBe("unverified");
    expect(["low", "medium"], "auto-extraction never sets high (FR-9)").toContain(entity.confidence);
    expect(entity.bank_or_chain).toBe("MBB");
    await expect.poll(() => stageOf(convId), { message: "payment details move the playbook to stall", timeout: 30_000 }).toBe("stall");
    // Held by a human: the bot must not have answered the account message.
    const d = (await (await api.get(`/internal/conversations/${convId}`)).json()) as ConversationDetail;
    const acctMsg = d.messages.find((m) => m.sender_type === "scammer" && m.text.includes(group4(account)));
    expect(acctMsg, "the account message is stored").toBeTruthy();
    const botAfter = d.messages.filter((m) => m.sender_type === "persona" && m.id > acctMsg!.id);
    expect(botAfter, "no bot reply after takeover").toEqual([]);
  });

  await test.step("the conversation is classified as a job scam", async () => {
    await expect
      .poll(async () => ((await (await api.get(`/internal/conversations/${convId}`)).json()) as ConversationDetail).conversation.scam_type, {
        message: "conversation scam_type",
        timeout: 30_000,
      })
      .toEqual({ scam_type: "job", valid: true });
  });

  await test.step("the scam phase history records every stage change (GET /internal/conversations/{id}/stages)", async () => {
    type Ev = { from_stage: string | null; to_stage: string; from_scam_type: string | null; scam_type: string | null; source: string; created_at: string };
    const h = (await (await api.get(`/internal/conversations/${convId}/stages`)).json()) as { stage: string; scam_type: string | null; events: Ev[] };
    expect(h.stage).toBe("stall");
    expect(h.scam_type).toBe("job");
    const moves = h.events.filter((e) => e.source === "bot").map((e) => [e.from_stage, e.to_stage]);
    expect(moves, `stage moves in order; events: ${JSON.stringify(h.events)}`).toEqual([
      [null, "rapport"],
      ["rapport", "bait"],
      ["bait", "payment_request"],
      ["payment_request", "stall"],
    ]);
    const classified = h.events.filter((e) => e.source === "classifier");
    expect(classified.length, "the classifier's label is recorded").toBeGreaterThan(0);
    expect(classified.at(-1)!.scam_type).toBe("job");
    const times = h.events.map((e) => Date.parse(e.created_at));
    expect([...times].sort((a, b) => a - b), "events come back oldest first").toEqual(times);
  });

  await test.step("before verification the bank gets no match (lookup policy A)", async () => {
    const res = await api.post("/v1/lookup", { data: { type: "bank_account", value: account, bank: "MBB" } });
    expect(res.status()).toBe(200);
    expect(await res.json()).toEqual({ match: false });
  });

  await test.step("operator verifies the entity (POST /internal/entities/{id}/verify)", async () => {
    const res = await api.post(`/internal/entities/${entity.id}/verify`, { data: { decision: "verified", note: `e2e flow ${scammer}` } });
    expect(res.status()).toBe(200);
    expect(await res.json()).toMatchObject({ id: entity.id, status: "verified", confidence: "high" });
  });

  await test.step("the bank's lookup returns a high-confidence job-scam hit (sell data to bank)", async () => {
    const res = await api.post("/v1/lookup", { data: { type: "bank_account", value: group4(account), bank: "MBB" } });
    expect(res.status()).toBe(200);
    const hit = (await res.json()) as { match: boolean; confidence: string; scam_types: string[]; sightings: number; first_seen: string; last_seen: string };
    expect(hit).toMatchObject({ match: true, status: "verified", confidence: "high", first_seen: expect.any(String), last_seen: expect.any(String) });
    expect(hit.scam_types).toContain("job");
    expect(hit.sightings).toBeGreaterThanOrEqual(1);
    const miss = await api.post("/v1/lookup", { data: { type: "bank_account", value: freshAccountNumber(), bank: "MBB" } });
    expect(await miss.json()).toEqual({ match: false });
  });

  await test.step("hand back to the bot and close", async () => {
    expect(await (await api.post(`/internal/conversations/${convId}/takeover`, { data: { mode: "bot" } })).json()).toEqual({ id: convId, status: "bot" });
    expect(await (await api.post(`/internal/conversations/${convId}/close`, { data: {} })).json()).toEqual({ id: convId, status: "closed" });
  });
});

test("d. AI summary of personas and active chats", async ({ page }) => {
  test.setTimeout(420_000);
  const scammer = freshScammer("sum");
  const llm = process.env.SOCKABUS_E2E_LLM ?? "unknown";
  let persona: Persona;
  let accountId = 0;
  let convId = 0;
  type Row = { id: number; summary: string; model: string; created_at: string };

  await test.step("an active persona in a live chat with a scammer", async () => {
    persona = await createActivePersona(api, "Summary", ["investment"]);
    cleanup.push(persona.id);
    accountId = await simAccountFor(api, persona.id);
    await simSend(api, accountId, scammer, "Hi aunty, I am Kevin from a crypto trading group. Our members earn 8% every week with USDT, very safe.");
    convId = await pollFor<Conversation[], number>(api, "/internal/conversations?limit=500", (l) => l.find((c) => c.scammer_handle === `@sim_${scammer}`)?.id, {
      what: "the summary conversation",
    });
    await waitForPersonaReplies(api, convId, 0);
    await simSend(api, accountId, scammer, "You can start with RM500 only. Transfer to my TRC20 wallet and I will open your account today.");
    await waitForPersonaReplies(api, convId, 1);
  });

  let convSummary: Row;
  await test.step("POST /internal/conversations/{id}/summary: Claude summarises the chat", async () => {
    const res = await api.post(`/internal/conversations/${convId}/summary`, { data: {}, timeout: 200_000 });
    const text = await res.text();
    expect(res.status(), `conversation summary → ${res.status()} ${text.slice(0, 300)}`).toBe(201);
    convSummary = (JSON.parse(text) as { summary: Row }).summary;
    expect(convSummary.summary.length, `a written summary (stack LLM: ${llm})`).toBeGreaterThan(80);
    if (llm === "anthropic") expect(convSummary.model).toMatch(/^anthropic\/claude-/);
    expect(convSummary.summary.toLowerCase(), "the summary is about this chat").toMatch(/invest|crypto|usdt|trading|wallet|return/);
    const got = (await (await api.get(`/internal/conversations/${convId}/summary`)).json()) as { summary: Row; stale: boolean };
    expect(got.summary.id).toBe(convSummary.id);
  });

  let personaSummary: Row;
  await test.step("POST /internal/personas/{id}/summary: the persona and its chats", async () => {
    const res = await api.post(`/internal/personas/${persona.id}/summary`, { data: {}, timeout: 200_000 });
    const text = await res.text();
    expect(res.status(), `persona summary → ${res.status()} ${text.slice(0, 300)}`).toBe(201);
    personaSummary = (JSON.parse(text) as { summary: Row }).summary;
    expect(personaSummary.summary.length).toBeGreaterThan(80);
  });

  await test.step("POST + GET /internal/summary: overview of active personas and chats", async () => {
    const res = await api.post("/internal/summary", { data: {}, timeout: 200_000 });
    const text = await res.text();
    expect(res.status(), `overview → ${res.status()} ${text.slice(0, 300)}`).toBe(201);
    const made = JSON.parse(text) as { active_personas: number; active_conversations: number; summary: Row };
    expect(made.active_personas).toBeGreaterThan(0);
    expect(made.active_conversations).toBeGreaterThan(0);
    expect(made.summary.summary.length).toBeGreaterThan(80);
    const got = (await (await api.get("/internal/summary")).json()) as { summary: Row; stale: boolean };
    expect(got.summary.id).toBe(made.summary.id);
    expect(got.stale).toBe(false);
  });

  await test.step("the dashboard card \"Summary · today\" shows the AI overview", async () => {
    // This test is in the api project (baseURL = Go API), so console pages use WEB_URL.
    await signIn(page, `${WEB_URL}/`);
    const card = page.getByTestId("overview-ai-summary-text");
    await expect(card).toBeVisible({ timeout: 120_000 });
    // The card shows the newest stored overview (it may regenerate one itself if the counts moved).
    await expect
      .poll(async () => (await card.textContent())?.trim() === ((await (await api.get("/internal/summary")).json()) as { summary: Row }).summary.summary.trim(), {
        message: "dashboard card text equals GET /internal/summary",
        timeout: 120_000,
      })
      .toBe(true);
  });

  await test.step("the persona detail shows its AI summary", async () => {
    await page.goto(`${WEB_URL}/personas?selected=${persona.id}`);
    await expect(page.getByTestId("persona-detail")).toBeVisible();
    await expect(page.getByTestId("persona-ai-summary-text")).toHaveText(personaSummary.summary.trim(), { timeout: 120_000 });
  });

  await test.step("the conversation view shows the chat's AI summary", async () => {
    await page.goto(`${WEB_URL}/conversations/${convId}`);
    await expect(page.getByTestId("convo-ai-summary-text")).toHaveText(/\S{3,}/, { timeout: 120_000 });
    const latest = ((await (await api.get(`/internal/conversations/${convId}/summary`)).json()) as { summary: Row }).summary;
    await expect(page.getByTestId("convo-ai-summary-text")).toHaveText(latest.summary.trim());
    await expect(page.getByTestId("stage-step").first(), "the stage bar reads the stage history").toHaveAttribute("data-reached-at", /\d{4}-\d{2}-\d{2}T/);
  });
});
