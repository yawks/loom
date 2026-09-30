import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { models } from "../wailsjs/go/models.ts";
import { getContactStatusEmoji } from "../src/lib/statusEmoji.ts";

const contact = (extra: string) => ({
  linkedAccounts: [{ extra, providerInstanceId: "provider-1" }],
}) as models.MetaContact;

test("status emoji uses the provider status text as its tooltip", () => {
  assert.deepEqual(getContactStatusEmoji(contact(JSON.stringify({
    statusEmoji: ":spiral_calendar_pad:",
    statusText: "En réunion",
  }))), {
    emoji: ":spiral_calendar_pad:",
    providerInstanceId: "provider-1",
    statusText: "En réunion",
  });
});

test("status emoji has no tooltip when the provider gives no text", () => {
  assert.equal(getContactStatusEmoji(contact('{"statusEmoji":":wave:"}'))?.statusText, undefined);
});
