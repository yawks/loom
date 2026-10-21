import { strict as assert } from "node:assert";
import { test } from "node:test";
import { canReadVisibleMessages } from "../src/lib/windowFocus.ts";

test("a pending read checks current native focus even before React receives blur", () => {
  const previousDocument = Object.getOwnPropertyDescriptor(globalThis, "document");
  let focused = true;
  let visibilityState = "visible";
  Object.defineProperty(globalThis, "document", {
    configurable: true,
    value: { hasFocus: () => focused, get visibilityState() { return visibilityState; } },
  });
  try {
    const pendingRead = () => canReadVisibleMessages();
    assert.equal(pendingRead(), true);
    focused = false;
    assert.equal(pendingRead(), false);
    focused = true;
    visibilityState = "hidden";
    assert.equal(pendingRead(), false);
    visibilityState = "visible";
    assert.equal(pendingRead(), true);
    Reflect.deleteProperty(globalThis, "document");
    assert.equal(pendingRead(), false);
  } finally {
    if (previousDocument) Object.defineProperty(globalThis, "document", previousDocument);
    else Reflect.deleteProperty(globalThis, "document");
  }
});
