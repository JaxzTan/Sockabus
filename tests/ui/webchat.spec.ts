// Demo path through the console's web chat page (TRD §9 UI regression). The console must point at
// a backend set up as for the API tests. If console sign-in is on, set SOCKABUS_CONSOLE_USER and
// SOCKABUS_CONSOLE_PASSWORD for the test run too.
import { expect, test, type APIRequestContext } from "@playwright/test";
import { type Conversation, freshAccountNumber, group4, operatorContext, pollFor } from "../helpers";

let api: APIRequestContext;
let personaId = 0;

test.beforeAll(async () => {
  api = await operatorContext();
  const gen = await api.post("/internal/personas/generate", { data: { count: 1, mode: "random" } });
  expect(gen.status()).toBe(201);
  personaId = ((await gen.json()) as { personas: { id: number }[] }).personas[0].id;
  expect((await api.post(`/internal/personas/${personaId}/status`, { data: { status: "active" } })).ok()).toBeTruthy();
});

test.afterAll(async () => {
  if (personaId) {
    const list = (await (await api.get("/internal/conversations?limit=100")).json()) as Conversation[];
    for (const c of list.filter((x) => x.persona_id === personaId && x.status !== "closed")) {
      await api.post(`/internal/conversations/${c.id}/close`, { data: {} });
    }
    await api.post(`/internal/personas/${personaId}/status`, { data: { status: "retired" } });
  }
  await api.dispose();
});

test("scammer chats on web chat, gets a reply, and the account is captured", async ({ page }) => {
  const account = freshAccountNumber();
  await page.goto(`/demo/chat?persona=${personaId}`);
  if (page.url().includes("/sign-in")) {
    const user = process.env.SOCKABUS_CONSOLE_USER;
    const password = process.env.SOCKABUS_CONSOLE_PASSWORD;
    test.skip(!user || !password, "console sign-in is on: set SOCKABUS_CONSOLE_USER and SOCKABUS_CONSOLE_PASSWORD");
    await page.getByTestId("signin-user").fill(user!);
    await page.getByTestId("signin-password").fill(password!);
    await page.getByTestId("signin-submit").click();
    await page.waitForURL(/\/demo\/chat/);
  }

  await expect(page.getByTestId("webchat-persona-select")).toHaveValue(String(personaId));
  await page.getByTestId("webchat-input").fill(`Part time job RM300 a day. Pay RM50 deposit to Maybank ${group4(account)} first`);
  await page.getByTestId("webchat-send-btn").click();

  const messages = page.getByTestId("webchat-message");
  await expect(messages.first()).toContainText(group4(account), { timeout: 15_000 });
  await expect(messages).toHaveCount(2, { timeout: 45_000 }); // scammer message + persona reply
  // The scammer's handle is captured too; look for the account's own row.
  await expect(page.getByTestId("webchat-captured").filter({ hasText: group4(account) })).toBeVisible({ timeout: 15_000 });

  // The same chat is visible to the operator through the API.
  await pollFor<Conversation[], number>(api, "/internal/conversations?limit=100", (list) =>
    list.find((c) => c.persona_id === personaId)?.id,
  );
});
