// Browser journeys through the console (web/), against the same real backend as the API tests.
// Each test builds its own data through the operator API with values generated per run, then
// drives and reads the real pages. Nothing is mocked or routed: the console proxies to the Go API.
import { expect, test, type APIRequestContext } from "@playwright/test";
import {
  type Conversation,
  type ConversationDetail,
  createActivePersona,
  type Entity,
  freshAccountNumber,
  freshScammer,
  group4,
  operatorContext,
  type Persona,
  pollFor,
  retirePersona,
  signIn,
  simAccountFor,
  simSend,
  waitForPersonaReplies,
} from "../helpers";

let api: APIRequestContext;
const cleanup: number[] = [];

test.beforeAll(async () => {
  api = await operatorContext();
});

test.afterAll(async () => {
  for (const id of cleanup) await retirePersona(api, id);
  await api.dispose();
});

test("e. bank simulator: a verified scam account is held, a clean account is sent", async ({ page }) => {
  const account = freshAccountNumber();
  const clean = freshAccountNumber();
  let persona: Persona;

  await test.step("capture and verify a scam account through the real pipeline", async () => {
    persona = await createActivePersona(api, "Bank");
    cleanup.push(persona.id);
    const acct = await simAccountFor(api, persona.id);
    await simSend(api, acct, freshScammer("bank"), `Part time job RM300 a day. Transfer RM50 deposit to Maybank ${group4(account)} first`);
    const e = await pollFor<Entity[], Entity>(api, "/internal/entities?limit=500", (l) => l.find((x) => x.type === "bank_account" && x.value_normalized === account), {
      timeoutMs: 60_000,
      what: `extracted account ${account}`,
    });
    const v = await api.post(`/internal/entities/${e.id}/verify`, { data: { decision: "verified", note: "e2e bank ui" } });
    expect(await v.json()).toMatchObject({ status: "verified", confidence: "high" });
  });

  await test.step("open the bank simulator", async () => {
    await signIn(page, "/demo/bank");
    await expect(page.getByTestId("account-input")).toBeVisible();
  });

  await test.step("transfer to the scam account: held, with the backend's high-confidence hit", async () => {
    await page.getByTestId("bank-select").selectOption("MBB");
    await page.getByTestId("account-input").fill(group4(account));
    const lookup = page.waitForResponse((r) => r.url().endsWith("/api/demo/lookup") && r.request().method() === "POST");
    await page.getByTestId("transfer-btn").click();
    const res = await lookup;
    expect(res.status()).toBe(200);
    const body = (await res.json()) as { res: { match: boolean; confidence: string; sightings: number }; ms: number };
    expect(body.res).toMatchObject({ match: true, confidence: "high" });

    const held = page.getByTestId("result-held");
    await expect(held).toBeVisible();
    await expect(held).toContainText("Transfer held");
    await expect(held).toContainText("High · human-verified");
    await expect(held).toContainText(`${body.ms} ms`);
    await expect(page.getByTestId("bank-why")).toContainText("Verified by an operator");
  });

  await test.step("transfer to a clean account: sent", async () => {
    await page.getByRole("button", { name: "Edit recipient" }).click();
    await page.getByTestId("account-input").fill(group4(clean));
    const lookup = page.waitForResponse((r) => r.url().endsWith("/api/demo/lookup") && r.request().method() === "POST");
    await page.getByTestId("transfer-btn").click();
    const res = await lookup;
    expect(((await res.json()) as { res: unknown }).res).toEqual({ match: false });
    const sent = page.getByTestId("result-sent");
    await expect(sent).toBeVisible();
    await expect(sent).toContainText("Transfer sent");
    await expect(page.getByTestId("result-held")).toHaveCount(0);
  });
});

test("f. web chat and conversations: messages appear in the console by polling", async ({ page, context }) => {
  const scammerText = `Part time job RM300 a day. Pay RM50 deposit to Maybank ${group4(freshAccountNumber())} first`;
  const account = scammerText.match(/\d{4} \d{4} \d{4}/)![0];
  let persona: Persona;
  let convId = 0;
  let accountId = 0;

  await test.step("an active persona to talk to", async () => {
    persona = await createActivePersona(api, "Chat");
    cleanup.push(persona.id);
  });

  await test.step("scammer sends a message in the web chat page", async () => {
    await signIn(page, `/demo/chat?persona=${persona.id}`);
    await expect(page.getByTestId("webchat-persona-select")).toHaveValue(String(persona.id));
    await page.getByTestId("webchat-input").fill(scammerText);
    await page.getByTestId("webchat-send-btn").click();
    await expect(page.getByTestId("webchat-message").first()).toContainText(account);
  });

  await test.step("the backend stored it: conversation and message via the API", async () => {
    const c = await pollFor<Conversation[], Conversation>(api, "/internal/conversations?limit=500", (l) => l.find((x) => x.persona_id === persona.id), {
      what: "the web chat conversation",
    });
    convId = c.id;
    accountId = c.account_id;
    const d = (await (await api.get(`/internal/conversations/${convId}`)).json()) as ConversationDetail;
    expect(d.messages[0]).toMatchObject({ sender_type: "scammer", text: scammerText });
  });

  await test.step("the persona's reply appears in the web chat", async () => {
    const replies = await waitForPersonaReplies(api, convId, 0);
    await expect(page.getByTestId("webchat-message").filter({ hasText: replies[0].text.split("\n")[0] })).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("webchat-captured").filter({ hasText: account })).toBeVisible({ timeout: 30_000 });
  });

  const convo = await context.newPage();
  await test.step("the conversations page shows the chat, its messages and the playbook stage", async () => {
    await convo.goto(`/conversations/${convId}`);
    await expect(convo.getByTestId("convo-row").filter({ hasText: persona.name })).toBeVisible();
    const msgs = convo.getByTestId("chat-message");
    await expect(msgs.filter({ hasText: account })).toBeVisible();
    const d = (await (await api.get(`/internal/conversations/${convId}`)).json()) as ConversationDetail;
    const firstReply = d.messages.find((m) => m.sender_type === "persona")!;
    await expect(msgs.filter({ hasText: firstReply.text.split("\n")[0] })).toBeVisible();
    // The account message moves the playbook to its last stage (stall).
    expect(d.conversation.stage).toBe("stall");
    await expect(convo.locator(".stages .stage.now")).toContainText("Stall");
  });

  await test.step("a new scammer message shows up on the open page without a reload (polling)", async () => {
    const before = await (await api.get(`/internal/conversations/${convId}`)).json() as ConversationDetail;
    const personaCount = before.messages.filter((m) => m.sender_type === "persona").length;
    const followUp = `Hello? Still there? Ref ${Date.now().toString(36)}`;
    const nav = convo.url();
    const handle = before.conversation.scammer_handle.replace(/^@sim_/, "");
    await simSend(api, accountId, handle, followUp);
    await expect(convo.getByTestId("chat-message").filter({ hasText: followUp })).toBeVisible({ timeout: 20_000 });
    // ...and the persona's next reply too.
    const next = await waitForPersonaReplies(api, convId, personaCount);
    await expect(convo.getByTestId("chat-message").filter({ hasText: next[0].text.split("\n")[0] }).last()).toBeVisible({ timeout: 20_000 });
    expect(convo.url(), "no navigation happened").toBe(nav);
  });

  await test.step("operator takes over from the console and replies", async () => {
    await convo.getByTestId("takeover-btn").click();
    await expect(convo.getByTestId("operator-input")).toBeVisible();
    await expect.poll(async () => ((await (await api.get(`/internal/conversations/${convId}`)).json()) as ConversationDetail).conversation.status).toBe("human");
    const text = `ok wait ah, i go bank ${Date.now().toString(36)}`;
    await convo.getByTestId("operator-input").fill(text);
    await convo.getByTestId("operator-send-btn").click();
    await pollFor<ConversationDetail, boolean>(api, `/internal/conversations/${convId}`, (d) => d.messages.some((m) => m.sender_type === "operator" && m.text === text), {
      what: "the operator message stored",
    });
    // The scammer sees it in the web chat page.
    await expect(page.getByTestId("webchat-message").filter({ hasText: text })).toBeVisible({ timeout: 20_000 });
  });
});
