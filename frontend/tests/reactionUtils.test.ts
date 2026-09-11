import { strict as assert } from "node:assert";
import { test } from "node:test";
import { normalizeReaction, reactionMatches } from "../src/lib/reactionUtils.ts";

test("persisted hearts and optimistic hearts share one group and removal identity", () => {
  const variants = ["❤", "❤️", "❤︎", ":❤:", ":❤️:", ":heart:", ":red_heart:"];
  const groups = new Set(variants.map((emoji) => normalizeReaction(emoji, false).storedEmoji));
  assert.deepEqual([...groups], [":heart:"]);
  for (const emoji of variants) {
    assert.ok(reactionMatches(emoji, "heart"));
    assert.equal(normalizeReaction(emoji, true).apiEmoji, "❤");
    assert.equal(normalizeReaction(emoji, false).apiEmoji, "heart");
  }
});

test("presentation variants preserve joined emoji and skin tone identities", () => {
  for (const emoji of ["❤️‍🔥", "🙋‍♂️", "🙋🏽‍♂️", "☀️"]) {
    const plain = emoji.replace(/[\uFE0E\uFE0F]/g, "");
    assert.equal(normalizeReaction(plain, false).storedEmoji, normalizeReaction(emoji, false).storedEmoji);
  }
  assert.notEqual(normalizeReaction("❤", false).storedEmoji, normalizeReaction("♥", false).storedEmoji);
  assert.notEqual(normalizeReaction("👍", false).storedEmoji, normalizeReaction("👍🏽", false).storedEmoji);
  assert.equal(normalizeReaction(":custom_heart:", false).apiEmoji, "custom_heart");
});
