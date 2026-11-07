// Screenshot walkthrough of the v1.0 demo flow through the console (PRD §10, blueprint TC-19).
// One numbered, full-page screenshot per stage, saved to results/screenshots/demo-flow/ and attached
// to the HTML report. Same rules as the rest of the suite: real backend, no mocks, values generated per run.
//   make test-ui ARGS='-g "demo flow screenshots"'      (from playwright/, stack up)
import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import path from "node:path";
import {
  type Conversation,
  type ConversationDetail,
  type Entity,
  freshAccountNumber,
  freshScammer,
  group4,
  operatorContext,
  pollFor,
  retirePersona,
  signIn,
  waitForPersonaReplies,
} from "../helpers";

const OUT = path.join(__dirname, "..", "results", "screenshots", "demo-flow");
let api: APIRequestContext;
let personaId = 0;

test.beforeAll(async () => {
  api = await operatorContext();
});
test.afterAll(async () => {
  await retirePersona(api, personaId);
  await api.dispose();
});

// Save a full-page PNG with a numbered name and attach it to the report step.
async function shot(page: Page, name: string) {
  await page.waitForLoadState("networkidle").catch(() => {}); // polling pages never go fully idle
  const file = path.join(OUT, `${name}.png`);
  await page.screenshot({ path: file, fullPage: true });
  await test.info().attach(name, { path: file, contentType: "image/png" });
}

test("demo flow screenshots: generate → chat → conversation → takeover → verify → bank → status", async ({ page, context }) => {
  test.setTimeout(420_000);
  const account = freshAccountNumber();
  const scammer = freshScammer("shots");
  let convId = 0;
  let entityId = 0;

  await test.step("1. Personas: generate a persona with one click", async () => {
    await signIn(page, "/personas");
    await shot(page, "01-personas-before");
    const gen = page.waitForResponse((r) => r.url().includes("/personas/generate") && r.request().method() === "POST", { timeout: 330_000 });
    await page.getByTestId("persona-generate-btn").click();
    const res = await gen;
    expect(res.status(), "generate from the console").toBe(201);
    const p = ((await res.json()) as { personas: { id: number; name: string }[] }).personas[0];
    personaId = p.id;
    await expect(page.getByTestId("persona-name").filter({ hasText: p.name }).first()).toBeVisible({ timeout: 30_000 });
    await shot(page, "02-personas-generated");
    const act = await api.post(`/internal/personas/${personaId}/status`, { data: { status: "active" } });
    expect(act.ok()).toBeTruthy();
  });

  await test.step("2. Web chat: the scammer messages the persona and sends a bank account", async () => {
    await page.goto(`/demo/chat?persona=${personaId}`);
    await expect(page.getByTestId("webchat-persona-select")).toHaveValue(String(personaId));
    await page.getByTestId("webchat-input").fill(`Hi auntie, part time job RM300 a day. Transfer RM50 deposit to Maybank ${group4(account)} first`);
    await page.getByTestId("webchat-send-btn").click();
    convId = await pollFor<Conversation[], number>(api, "/internal/conversations?limit=500", (l) => l.find((c) => c.persona_id === personaId)?.id, {
      what: "the web chat conversation",
    });
    const [reply] = await waitForPersonaReplies(api, convId, 0);
    await expect(page.getByTestId("webchat-message").filter({ hasText: reply.text.split("\n")[0] })).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("webchat-captured").filter({ hasText: group4(account) })).toBeVisible({ timeout: 30_000 });
    await shot(page, "03-webchat-reply-and-captured-account");
  });

  const convo = await context.newPage();
  await test.step("3. Conversations: the operator watches the live chat", async () => {
    await convo.goto(`/conversations/${convId}`);
    await expect(convo.getByTestId("chat-message").filter({ hasText: group4(account) })).toBeVisible();
    await shot(convo, "04-conversation-live");
  });

  await test.step("4. Takeover: the operator takes the chat and replies", async () => {
    await convo.getByTestId("takeover-btn").click();
    await expect(convo.getByTestId("operator-input")).toBeVisible();
    const text = `ok wait ah, i go bank first ${scammer.slice(-4)}`;
    await convo.getByTestId("operator-input").fill(text);
    await convo.getByTestId("operator-send-btn").click();
    await expect(convo.getByTestId("chat-message").filter({ hasText: text })).toBeVisible({ timeout: 20_000 });
    await shot(convo, "05-conversation-takeover");
  });

  await test.step("5. Verify queue: the operator verifies the captured account", async () => {
    entityId = (
      await pollFor<ConversationDetail, Entity>(api, `/internal/conversations/${convId}`, (d) =>
        d.entities.find((e) => e.type === "bank_account" && e.value_normalized === account),
      )
    ).id;
    await page.goto("/verify");
    const row = page.getByTestId("entity-row").filter({ hasText: new RegExp(account.slice(-4)) });
    await expect(row.first()).toBeVisible({ timeout: 30_000 });
    await row.first().click();
    await shot(page, "06-verify-queue");
    await page.getByTestId("verify-btn").click();
    await expect
      .poll(async () => ((await (await api.get(`/internal/conversations/${convId}`)).json()) as ConversationDetail).entities.find((e) => e.id === entityId)?.status)
      .toBe("verified");
    await shot(page, "07-verify-done");
  });

  await test.step("6. Bank simulator: a transfer to the verified account is held", async () => {
    await page.goto("/demo/bank");
    await page.getByTestId("bank-select").selectOption("MBB");
    await page.getByTestId("account-input").fill(group4(account));
    await page.getByTestId("transfer-btn").click();
    await expect(page.getByTestId("result-held")).toBeVisible({ timeout: 20_000 });
    await shot(page, "08-bank-transfer-held");
  });

  await test.step("7. Status page: bot and endpoints", async () => {
    await page.goto("/status");
    await expect(page.getByTestId("status-workflows")).toBeVisible();
    await shot(page, "09-status");
  });
});
