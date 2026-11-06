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

test("native focus overrides a WebView that still claims focus", async () => {
  const { confirmReadWindowFocus } = await import("../src/lib/windowFocus.ts");
  const previousDocument = Object.getOwnPropertyDescriptor(globalThis, "document");
  const previousWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  let nativeActive = false;
  let focused = true;
  Object.defineProperty(globalThis, "document", { configurable: true, value: {
    hasFocus: () => focused, visibilityState: "visible",
  } });
  Object.defineProperty(globalThis, "window", { configurable: true, value: {
    go: { main: { App: { IsWindowActive: async () => nativeActive } } },
  } });
  try {
    assert.equal(await confirmReadWindowFocus(), false);
    nativeActive = true;
    assert.equal(await confirmReadWindowFocus(), true);
    focused = false;
    assert.equal(await confirmReadWindowFocus(), false);
    focused = true;
    (window.go!.main!.App as Record<string, unknown>).IsWindowActive = async () => {
      focused = false;
      return true;
    };
    assert.equal(await confirmReadWindowFocus(), false);
    focused = true;
    (window.go!.main!.App as Record<string, unknown>).IsWindowActive = async () => { throw new Error("offline"); };
    assert.equal(await confirmReadWindowFocus(), false);
  } finally {
    if (previousDocument) Object.defineProperty(globalThis, "document", previousDocument);
    else Reflect.deleteProperty(globalThis, "document");
    if (previousWindow) Object.defineProperty(globalThis, "window", previousWindow);
    else Reflect.deleteProperty(globalThis, "window");
  }
});
