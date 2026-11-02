// A message whose stored ciphertext no longer decrypts (corrupted row, wrong key) must not break the
// conversation: the API returns it as a placeholder with undecryptable: true, the console shows a muted
// "could not be decrypted" bubble, the other messages still show, and the persona keeps replying.
// Setup corrupts one real message on the test database (SOCKABUS_E2E_PSQL); nothing is mocked.
import { expect, test, type APIRequestContext } from "@playwright/test";
import {
  type ConversationDetail,
  type Conversation,
  createActivePersona,
  freshScammer,
  operatorContext,
  type Persona,
  pollFor,
  psql,
  retirePersona,
  signIn,
  simAccountFor,
  simSend,
  waitForPersonaReplies,
} from "../helpers";

let api: APIRequestContext;
let persona: Persona | undefined;

test.beforeAll(async () => {
  api = await operatorContext();
});

test.afterAll(async () => {
  if (persona) await retirePersona(api, persona.id);
  await api.dispose();
});

test("g. undecryptable message: the conversation still loads with a placeholder", async ({ page }) => {
  const scammer = freshScammer("crypt");
  const first = `Hi madam, part time job, like hotel reviews, RM300 a day. Ref ${Date.now().toString(36)}`;
  const second = `Very easy, just need your phone. Interested? Ref ${Date.now().toString(36)}b`;
  let convId = 0;
  let accountId = 0;
  let corruptedId = 0;

  await test.step("a real conversation: two scammer messages and the persona's reply", async () => {
    persona = await createActivePersona(api, "Crypt");
    accountId = await simAccountFor(api, persona.id);
    await simSend(api, accountId, scammer, first);
    convId = (
      await pollFor<Conversation[], Conversation>(api, "/internal/conversations?limit=500", (l) => l.find((c) => c.scammer_handle === `@sim_${scammer}`), {
        what: "the conversation",
      })
    ).id;
    await waitForPersonaReplies(api, convId, 0);
    await simSend(api, accountId, scammer, second);
    await pollFor<ConversationDetail, boolean>(api, `/internal/conversations/${convId}`, (d) => d.messages.some((m) => m.text === second), {
      what: "the second scammer message",
    });
    const d = (await (await api.get(`/internal/conversations/${convId}`)).json()) as ConversationDetail;
    corruptedId = d.messages.find((m) => m.text === first)!.id;
    expect(corruptedId).toBeGreaterThan(0);
  });

  await test.step("corrupt the first message's stored ciphertext (flip one byte of the GCM tag)", async () => {
    // messages is append-only: the trigger is disabled only inside this one transaction.
    psql(`BEGIN;
ALTER TABLE messages DISABLE TRIGGER messages_append_only;
UPDATE messages SET body_ciphertext = set_byte(body_ciphertext, length(body_ciphertext) - 1, get_byte(body_ciphertext, length(body_ciphertext) - 1) # 1)
WHERE id = ${corruptedId} AND conversation_id = ${convId};
ALTER TABLE messages ENABLE TRIGGER messages_append_only;
COMMIT;`);
  });

  await test.step("the API still returns the conversation, with that message flagged", async () => {
    const res = await api.get(`/internal/conversations/${convId}`);
    expect(res.status(), await res.text()).toBe(200);
    const d = (await res.json()) as ConversationDetail;
    const bad = d.messages.find((m) => m.id === corruptedId)!;
    expect(bad).toMatchObject({ undecryptable: true, text: "[message could not be decrypted]", sender_type: "scammer" });
    expect(d.messages.find((m) => m.text === second)?.undecryptable).toBeFalsy();
    expect(d.messages.filter((m) => m.undecryptable)).toHaveLength(1);
    expect(d.conversation.scammer_handle).toBe(`@sim_${scammer}`);
  });

  await test.step("the conversations page loads and shows a muted placeholder bubble", async () => {
    await signIn(page, `/conversations/${convId}`);
    const bad = page.locator('[data-testid="chat-message"][data-undecryptable="true"]');
    await expect(bad).toHaveCount(1);
    await expect(bad).toContainText("Message could not be decrypted");
    await expect(page.getByTestId("chat-message").filter({ hasText: second })).toBeVisible();
    await expect(page.getByTestId("chat-message").filter({ hasText: first })).toHaveCount(0);
  });

  await test.step("the persona keeps replying and the AI summary still works", async () => {
    const before = ((await (await api.get(`/internal/conversations/${convId}`)).json()) as ConversationDetail).messages.filter((m) => m.sender_type === "persona").length;
    await simSend(api, accountId, scammer, `Hello? You there? Ref ${Date.now().toString(36)}c`);
    await waitForPersonaReplies(api, convId, before);
    const sum = await api.post(`/internal/conversations/${convId}/summary`, { data: {}, timeout: 300_000 });
    expect(sum.status(), await sum.text()).toBeLessThan(300);
    const body = (await sum.json()) as { summary: { summary: string } | null };
    expect(body.summary?.summary ?? "").not.toBe("");
    expect(body.summary?.summary).not.toContain("could not be decrypted");
  });
});
