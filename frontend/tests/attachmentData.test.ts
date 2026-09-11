import { strict as assert } from "node:assert";
import { test } from "node:test";
import { dataUrlToBytes } from "../src/lib/attachmentData.ts";

test("Office and PDF attachment bytes are preserved without fetching a data URL", () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = () => { throw new Error("Not allowed to load local resource"); };
  try {
    const expected = Uint8Array.from([0x50, 0x4b, 3, 4, 0, 0x80, 0xff]);
    for (const mime of [
      "application/vnd.openxmlformats-officedocument.presentationml.presentation",
      "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
      "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
      "application/pdf",
    ]) {
      const data = `data:${mime};base64,${Buffer.from(expected).toString("base64")}`;
      const bytes = dataUrlToBytes(data);
      assert.deepEqual(bytes, expected);
      assert.deepEqual(new Uint8Array(bytes.buffer), expected);
    }
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("non-base64 text data remains supported", () => {
  assert.equal(new TextDecoder().decode(dataUrlToBytes("data:text/plain;charset=utf-8,caf%C3%A9")), "café");
});

test("invalid attachment payloads fail without leaking their contents", () => {
  for (const value of ["https://example.test/file", "", "data:application/pdf;base64"]) {
    assert.throws(() => dataUrlToBytes(value), /Invalid attachment data URL/);
  }
  assert.throws(() => dataUrlToBytes("data:application/pdf;base64,%%%"));
});
