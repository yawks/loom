import { strict as assert } from "node:assert";
import { test } from "node:test";
import { getSenderDisplayName } from "../src/lib/messageUtils.ts";

test("own phone number falls back to the translated self label", () => {
  const translate = (key: string) => key === "you" ? "Vous" : key;

  assert.equal(getSenderDisplayName("+33 6 50 40 12 44", "self", true, translate), "Vous");
  assert.equal(getSenderDisplayName("Mathieu", "self", true, translate), "Mathieu");
});
