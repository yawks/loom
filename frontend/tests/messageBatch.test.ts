import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { models } from "../wailsjs/go/models";
import { mergeMessageBatch } from "../src/lib/messageBatch.ts";
import { addPendingMessage, mergePendingMessages } from "../src/lib/pendingMessages";

const message = (id: string, body = id) => ({ protocolMsgId: id, body }) as models.Message;

test("incoming batch appears immediately and survives a stale database response", () => {
  const old = message("old");
  const incoming = message("incoming");
  const current = { pages: [[old]], pageParams: [undefined] };
  addPendingMessage("batch-test", incoming);
  const updated = mergeMessageBatch(current, [incoming])!;
  assert.deepEqual(updated.pages[0], [old, incoming]);
  assert.deepEqual(current.pages[0], [old]);
  assert.deepEqual(mergePendingMessages("batch-test", [old]), [old, incoming]);
  assert.deepEqual(mergePendingMessages("batch-test", [old, incoming]), [old, incoming]);
});

test("batch updates older pages without duplicates or resetting pagination", () => {
  const current = { pages: [[message("recent")], [message("older")]], pageParams: [undefined, "cursor"] };
  const updated = mergeMessageBatch(current, [message("older", "edited"), message("new")])!;
  assert.deepEqual(updated.pages.map((page) => page.map((item) => item.body)), [["recent", "new"], ["edited"]]);
  assert.deepEqual(updated.pageParams, current.pageParams);
  assert.equal(mergeMessageBatch(undefined, [message("new")]), undefined);
});

test("confirmed send is inserted when a refetch has removed its placeholder", () => {
  const confirmed = message("confirmed");
  const current = { pages: [[message("old")]], pageParams: [undefined] };
  const updated = mergeMessageBatch(current, [confirmed], "temp-send")!;
  assert.deepEqual(updated.pages[0].map((item) => item.protocolMsgId), ["old", "confirmed"]);
});

test("confirmed send removes its placeholder and deduplicates a provider echo", () => {
  const confirmed = message("confirmed");
  const current = { pages: [[message("temp-send"), confirmed]], pageParams: [undefined] };
  const updated = mergeMessageBatch(current, [confirmed], "temp-send")!;
  assert.deepEqual(updated.pages[0], [confirmed]);
  assert.equal(current.pages[0].length, 2);
});

test("an in-flight history fetch cannot overwrite the optimistic send after cancellation", async () => {
  const { QueryClient } = await import("@tanstack/react-query");
  const client = new QueryClient();
  const key = ["messages", "send-race-test"];
  const current = { pages: [[message("old")]], pageParams: [undefined] };
  client.setQueryData(key, current);
  let finishFetch!: (data: typeof current) => void;
  const fetch = client.fetchQuery({
    queryKey: key,
    queryFn: () => new Promise<typeof current>((resolve) => { finishFetch = resolve; }),
  }).catch(() => undefined);
  await client.cancelQueries({ queryKey: key, exact: true });
  const optimistic = mergeMessageBatch(current, [message("temp-send")])!;
  client.setQueryData(key, optimistic);
  finishFetch(current);
  await fetch;
  assert.deepEqual(client.getQueryData(key), optimistic);
  client.clear();
});
