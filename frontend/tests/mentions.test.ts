import { strict as assert } from "node:assert";
import { test } from "node:test";
import { resolveMentionPositions } from "../src/lib/mentions.ts";

test("mentions are resolved in text order even when selected out of order", () => {
  const mentions = [
    { userId: "bob", displayName: "Bob", start: 15, length: 4 },
    { userId: "amy", displayName: "Amy", start: 0, length: 4 },
  ];

  assert.deepEqual(resolveMentionPositions("@Amy bonjour @Bob", mentions), [
    { userId: "amy", displayName: "Amy", start: 0, length: 4 },
    { userId: "bob", displayName: "Bob", start: 13, length: 4 },
  ]);
});
