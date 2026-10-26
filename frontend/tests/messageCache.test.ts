import { strict as assert } from "node:assert";
import { test } from "node:test";
import { QueryClient } from "@tanstack/react-query";
import type { models } from "../wailsjs/go/models";
import { confirmThreadMessage, updateCachedMessage } from "../src/lib/messageCache.ts";
import { addPendingMessage, mergePendingMessages, removePendingMessage, threadPendingKey } from "../src/lib/pendingMessages.ts";

test("thread send survives stale refetches and confirmation without its placeholder", () => {
  const key = threadPendingKey("send-conversation", "parent");
  const temporary = { protocolMsgId: "temp-send", body: "reply" } as models.Message;
  const confirmed = { protocolMsgId: "confirmed", body: "reply" } as models.Message;
  addPendingMessage(key, temporary);
  assert.deepEqual(mergePendingMessages(key, []), [temporary]);
  assert.deepEqual(mergePendingMessages(threadPendingKey("send-conversation", "other"), []), []);
  assert.deepEqual(mergePendingMessages(threadPendingKey("other", "parent"), []), []);

  removePendingMessage(key, temporary.protocolMsgId);
  addPendingMessage(key, confirmed);
  assert.deepEqual(confirmThreadMessage([], confirmed, temporary.protocolMsgId), [confirmed]);
  assert.deepEqual(confirmThreadMessage([temporary, confirmed], confirmed, temporary.protocolMsgId), [confirmed]);
  assert.deepEqual(mergePendingMessages(key, []), [confirmed]);
  assert.deepEqual(mergePendingMessages(key, [confirmed]), [confirmed]);
  // Once persistence confirms the message, it no longer lives in the bridge.
  assert.deepEqual(mergePendingMessages(key, []), []);
});

test("failed thread sends can remove their pending placeholder", () => {
  const key = threadPendingKey("failed-send", "parent");
  addPendingMessage(key, { protocolMsgId: "temp-failed" } as models.Message);
  removePendingMessage(key, "temp-failed");
  assert.deepEqual(mergePendingMessages(key, []), []);
});

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
