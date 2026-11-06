import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { models } from "../wailsjs/go/models";
import { useMessageReadStore, latestOwnActivityTimestamp, reconcilePersistedUnreadMessages } from "../src/lib/messageReadStore";

const message = (id: string, threadId = "", isFromMe = false) => ({
  protocolConvId: "conversation", protocolMsgId: id, threadId, isFromMe,
  timestamp: "2026-10-13T10:00:00Z",
}) as unknown as models.Message;

for (const batched of [false, true]) {
  for (const threadId of ["", "thread-b"]) {
    test(`${batched ? "batch" : "live"} sending in ${threadId || "main"} preserves other unread threads`, () => {
      const store = useMessageReadStore.getState();
      useMessageReadStore.setState({ readByConversation: {} });
      store.registerIncomingMessage(message("reply-a", "thread-a"));
      store.registerIncomingMessage(message("main"));
      const own = message("own", threadId, true);
      if (batched) store.registerBatchMessages([own]);
      else store.registerIncomingMessage(own);
      const state = useMessageReadStore.getState().readByConversation.conversation;
      assert.equal(state["reply-a"], false);
      assert.equal(state.main, threadId === "");
      store.markAsReadSilently("conversation", "reply-a");
      assert.equal(useMessageReadStore.getState().readByConversation.conversation["reply-a"], true);
    });
  }
}

test("synchronizing main activity preserves unread replies", () => {
  const store = useMessageReadStore.getState();
  useMessageReadStore.setState({ readByConversation: {} });
  const reply = message("reply-a", "thread-a");
  store.registerIncomingMessage(reply);
  store.syncConversation("conversation", [reply, message("own", "", true)]);
  assert.equal(useMessageReadStore.getState().readByConversation.conversation["reply-a"], false);
  assert.equal(latestOwnActivityTimestamp([message("own-thread", "thread-b", true)]), undefined);
});

for (const path of ["live", "batch", "sync"] as const) {
  test(`${path} deleted replies clear their unread state and preserve other replies`, () => {
    const store = useMessageReadStore.getState();
    useMessageReadStore.setState({ readByConversation: {} });
    store.registerIncomingMessage(message("deleted", "thread-a"));
    store.registerIncomingMessage(message("other", "thread-b"));
    const deleted = { ...message("deleted", "thread-a"), isDeleted: true } as models.Message;
    if (path === "live") store.registerIncomingMessage(deleted);
    else if (path === "batch") store.registerBatchMessages([deleted], false, false, true);
    else store.syncConversation("conversation", [deleted]);
    const state = useMessageReadStore.getState().readByConversation.conversation;
    assert.equal(state.deleted, true);
    assert.equal(state.other, false);
    // A repeated delivery must not recreate an unread item.
    store.registerIncomingMessage(deleted);
    assert.equal(useMessageReadStore.getState().readByConversation.conversation.deleted, true);
  });
}

test("automatic reads preserve local unread state when native macOS focus is false", async () => {
  const previousDocument = Object.getOwnPropertyDescriptor(globalThis, "document");
  const previousWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  let active = false;
  let receipts = 0;
  Object.defineProperty(globalThis, "document", { configurable: true, value: {
    visibilityState: "visible", hasFocus: () => true,
  } });
  Object.defineProperty(globalThis, "window", { configurable: true, value: {
    go: { main: { App: {
      IsWindowActive: async () => active,
      MarkMessageAsRead: async () => { receipts++; },
    } } },
  } });
  try {
    const store = useMessageReadStore.getState();
    useMessageReadStore.setState({ readByConversation: {} });
    store.registerIncomingMessage(message("main"));
    store.registerIncomingMessage(message("reply", "thread"));
    await store.markAsRead("conversation", "main");
    await store.markMultipleAsRead("conversation", ["reply"]);
    assert.equal(useMessageReadStore.getState().readByConversation.conversation.main, false);
    assert.equal(useMessageReadStore.getState().readByConversation.conversation.reply, false);
    assert.equal(receipts, 0);
    active = true;
    await store.markAsRead("conversation", "main");
    await store.markMultipleAsRead("conversation", ["reply"]);
    assert.equal(useMessageReadStore.getState().readByConversation.conversation.main, true);
    assert.equal(useMessageReadStore.getState().readByConversation.conversation.reply, true);
    assert.equal(receipts, 2);
  } finally {
    if (previousDocument) Object.defineProperty(globalThis, "document", previousDocument);
    else Reflect.deleteProperty(globalThis, "document");
    if (previousWindow) Object.defineProperty(globalThis, "window", previousWindow);
    else Reflect.deleteProperty(globalThis, "window");
  }
});

test("startup repairs persisted deleted replies without reading other messages", async () => {
  useMessageReadStore.setState({ readByConversation: {
    conversation: {
      "1793791400.602969": false, "_thread:1793791400.602969": true,
      other: false, "_thread:other": true, missing: false, read: true,
    },
    foreign: { reply: false },
  } });
  const calls: string[] = [];
  await reconcilePersistedUnreadMessages(async (conversationId, ids) => {
    calls.push(conversationId);
    if (conversationId === "foreign") return [{ messageId: "reply", isDeleted: false }];
    assert.deepEqual(ids, ["1793791400.602969", "other", "missing"]);
    return [
      { messageId: "1793791400.602969", isDeleted: true },
      { messageId: "other", isDeleted: false },
    ];
  });
  const state = useMessageReadStore.getState().readByConversation;
  assert.equal(state.conversation["1793791400.602969"], undefined);
  assert.equal(state.conversation["_thread:1793791400.602969"], undefined);
  assert.equal(state.conversation.other, false);
  assert.equal(state.conversation.missing, false);
  assert.equal(state.conversation.read, true);
  assert.equal(state.foreign.reply, false);
  assert.deepEqual(calls, ["conversation", "foreign"]);
});

test("startup reconciliation continues after a failed conversation query", async () => {
  useMessageReadStore.setState({ readByConversation: {
    failed: { reply: false }, repaired: { deleted: false },
  } });
  await reconcilePersistedUnreadMessages(async (conversationId) => {
    if (conversationId === "failed") throw new Error("database unavailable");
    return [{ messageId: "deleted", isDeleted: true }];
  });
  assert.equal(useMessageReadStore.getState().readByConversation.failed.reply, false);
  assert.equal(useMessageReadStore.getState().readByConversation.repaired.deleted, undefined);
});
