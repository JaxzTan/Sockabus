// Demo path through the console's web chat page (TRD §9 UI regression). The console must point at
// a backend set up as for the API tests. If console sign-in is on, set SOCKABUS_SESSION_SECRET (and
// SOCKABUS_E2E_CONSOLE_EMAIL if not the default) for the test run too (it fails without them; never skips).
import { expect, test, type APIRequestContext } from "@playwright/test";
import { type Conversation, freshAccountNumber, generateActivePersona, group4, operatorContext, pollFor, retirePersona, signIn } from "../helpers";

let api: APIRequestContext;
let personaId = 0;

test.beforeAll(async () => {
  api = await operatorContext();
});

test.afterAll(async () => {
  await retirePersona(api, personaId);
  await api.dispose();
});

test("scammer chats on web chat, gets a reply, and the account is captured", async ({ page }) => {
  test.setTimeout(420_000);
  const account = freshAccountNumber();
  await test.step("generate and activate a persona", async () => {
    personaId = await generateActivePersona(api);
  });

  await test.step("send the scam message from the web chat page", async () => {
    await signIn(page, `/demo/chat?persona=${personaId}`);
    await expect(page.getByTestId("webchat-persona-select")).toHaveValue(String(personaId));
    await page.getByTestId("webchat-input").fill(`Part time job RM300 a day. Pay RM50 deposit to Maybank ${group4(account)} first`);
    await page.getByTestId("webchat-send-btn").click();
  });

  await test.step("the persona replies and the account is captured", async () => {
    const messages = page.getByTestId("webchat-message");
    await expect(messages.first()).toContainText(group4(account), { timeout: 15_000 });
    // The scammer message plus at least one persona bubble (a reply may be split into several).
    await expect.poll(() => messages.count(), { timeout: 90_000 }).toBeGreaterThanOrEqual(2);
    // The scammer's handle is captured too; look for the account's own row.
    await expect(page.getByTestId("webchat-captured").filter({ hasText: group4(account) })).toBeVisible({ timeout: 15_000 });
  });

  await test.step("the same chat is visible to the operator through the API", async () => {
    await pollFor<Conversation[], number>(api, "/internal/conversations?limit=100", (list) => list.find((c) => c.persona_id === personaId)?.id);
  });
});
