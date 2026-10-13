import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { models } from "../wailsjs/go/models";
import { useMessageReadStore, latestOwnActivityTimestamp } from "../src/lib/messageReadStore";

const message = (id: string, threadId = "", isFromMe = false) => ({
  protocolConvId: "conversation", protocolMsgId: id, threadId, isFromMe,
  timestamp: "2026-10-13T10:00:00Z",
}) as models.Message;

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
