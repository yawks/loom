import { strict as assert } from "node:assert";
import { test } from "node:test";
import { reactionHaloKey, useReactionHaloStore } from "../src/lib/reactionHaloStore.ts";

test("acknowledged reactions keep independent halos for each message and account", () => {
  useReactionHaloStore.setState({ pending: {} });
  const store = useReactionHaloStore.getState();
  store.register([
    { id: 1, conversationId: "one::chat", messageId: "sent", emoji: "👍" },
    { id: 2, conversationId: "two::chat", messageId: "sent", emoji: "👍" },
    { id: 3, conversationId: "one::chat", messageId: "other", emoji: "❤️" },
  ]);
  const key = reactionHaloKey("one::chat", "sent", ":+1:");
  assert.equal(useReactionHaloStore.getState().pending[key], 1);
  store.finish(key, 1);
  assert.equal(useReactionHaloStore.getState().pending[key], undefined);
  assert.equal(Object.keys(useReactionHaloStore.getState().pending).length, 2);
  assert.equal(useReactionHaloStore.getState().pending[reactionHaloKey("two::chat", "sent", "👍")], 2);
});

test("an expiring halo cannot erase a newer reaction on the same pastille", () => {
  useReactionHaloStore.setState({ pending: {} });
  const store = useReactionHaloStore.getState();
  const activity = { id: 10, conversationId: "one::chat", messageId: "sent", emoji: "❤" };
  store.register([activity]);
  const key = reactionHaloKey(activity.conversationId, activity.messageId, ":heart:");
  store.register([{ ...activity, id: 11, emoji: "❤️" }]);
  store.finish(key, 10);
  assert.equal(useReactionHaloStore.getState().pending[key], 11);
  store.finish(key, 11);
  assert.deepEqual(useReactionHaloStore.getState().pending, {});
});

test("presentation aliases share a halo but unrelated custom emojis do not", () => {
  const key = (emoji: string) => reactionHaloKey("chat", "message", emoji);
  assert.equal(key("👍"), key(":+1:"));
  assert.equal(key("❤️"), key(":heart:"));
  assert.equal(key(":+1::skin-tone-2:"), key(":+1:"));
  assert.notEqual(key(":custom_heart:"), key("❤️"));
});
