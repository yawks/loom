import { strict as assert } from "node:assert";
import { test } from "node:test";
import { QueryClient } from "@tanstack/react-query";
import type { models } from "../wailsjs/go/models";
import { updateCachedMessage } from "../src/lib/messageCache.ts";

test("edits and rollback reach conversation and thread caches without changing other conversations", () => {
  const client = new QueryClient();
  const original = { protocolMsgId: "reply", body: "before", isEdited: false } as models.Message;
  const other = { ...original, protocolMsgId: "other" } as models.Message;
  const messagesKey = ["messages", "conversation"];
  const threadKey = ["threads", "conversation", "parent"];
  const otherThreadKey = ["threads", "elsewhere", "parent"];
  const data = { pages: [[other], [original]], pageParams: [null, "cursor"] };
  client.setQueryData(messagesKey, data);
  client.setQueryData(threadKey, [original, other]);
  client.setQueryData(otherThreadKey, [original]);

  updateCachedMessage(client, "conversation", "reply", "after", true);
  const edited = { ...original, body: "after", isEdited: true };
  assert.deepEqual(client.getQueryData(messagesKey), { ...data, pages: [[other], [edited]] });
  assert.deepEqual(client.getQueryData(threadKey), [edited, other]);
  assert.deepEqual(client.getQueryData(otherThreadKey), [original]);
  assert.equal(original.body, "before");

  updateCachedMessage(client, "conversation", "reply", "before", false);
  assert.deepEqual(client.getQueryData(messagesKey), data);
  assert.deepEqual(client.getQueryData(threadKey), [original, other]);
  client.clear();
});

test("a thread reply can be edited before the conversation cache has loaded", () => {
  const client = new QueryClient();
  const key = ["threads", "conversation", "parent"];
  client.setQueryData(key, [{ protocolMsgId: "reply", body: "before" }]);
  updateCachedMessage(client, "conversation", "reply", "after", true);
  assert.deepEqual(client.getQueryData(key), [{ protocolMsgId: "reply", body: "after", isEdited: true }]);
  assert.equal(client.getQueryData(["messages", "conversation"]), undefined);
  client.clear();
});
