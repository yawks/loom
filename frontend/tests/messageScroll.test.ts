import { strict as assert } from "node:assert";
import { test } from "node:test";
import { openingMessageScrollTop } from "../src/lib/messageScroll.ts";

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
