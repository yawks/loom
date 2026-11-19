import { strict as assert } from "node:assert";
import { test } from "node:test";
import { isAtMessageListBottom, openingMessageScrollTop } from "../src/lib/messageScroll.ts";

test("opening a timeline without unread messages reaches its actual bottom", () => {
  assert.equal(openingMessageScrollTop(3000, 600), 2400);
  assert.equal(openingMessageScrollTop(300, 600), 0);
});

test("a tall unread block starts at the unread boundary", () => {
  assert.equal(openingMessageScrollTop(3000, 600, 1200), 1200);
  assert.equal(openingMessageScrollTop(4000, 600, 1200), 1200);
});

test("unread content that fits opens at the bottom without extra empty space", () => {
  assert.equal(openingMessageScrollTop(3000, 600, 2700), 2400);
  assert.equal(openingMessageScrollTop(3000, 600, 2400), 2400);
  assert.equal(openingMessageScrollTop(300, 600, 100), 0);
});

test("a click without scrolling preserves the live edge, but dragging away does not", () => {
  assert.equal(isAtMessageListBottom(3000, 2400, 600), true);
  assert.equal(isAtMessageListBottom(3000, 2399, 600), true);
  assert.equal(isAtMessageListBottom(3000, 2300, 600), false);
  assert.equal(isAtMessageListBottom(300, 0, 600), true);
});
