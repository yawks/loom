import { strict as assert } from "node:assert";
import { test } from "node:test";
import { BrowserOpenURL } from "../src/lib/browser.ts";

test("external links reach Wails with punctuation encoded and query values intact", () => {
  const opened: string[] = [];
  const previousWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: { runtime: { BrowserOpenURL: (url: string) => opened.push(url) } },
  });
  try {
    const urls = [
      "https://example.com/issues?q=(-%7BIT%20Support%7D%20%23Unresolved%20)%20and%20(project:%20Example)",
      "https://example.com/path?q=[]{}!*$;|`~<>\\ value&next=1#section",
      "https://example.com/path?q=%28already%20encoded%29&next=1",
      "mailto:person@example.com",
      "tel:+33123456789",
    ];
    for (const url of urls) {
      BrowserOpenURL(url);
      const result = opened.at(-1)!;
      assert.doesNotMatch(result, /[;|`$\\<>*{}\[\]()~! ]/);
      const original = new URL(url);
      const encoded = new URL(result);
      assert.equal(encoded.protocol, original.protocol);
      assert.equal(encoded.host, original.host);
      assert.equal(encoded.searchParams.toString(), original.searchParams.toString());
      assert.equal(encoded.hash, original.hash);
    }
    assert.equal(opened[0], urls[0].replaceAll("(", "%28").replaceAll(")", "%29"));
    assert.equal(opened[2], urls[2]);
    // Leave disallowed schemes and control characters visible to Wails validation.
    BrowserOpenURL("javascript:alert(1)");
    assert.equal(opened.at(-1), "javascript:alert%281%29");
    BrowserOpenURL("https://example.com/\u0000\n");
    assert.equal(opened.at(-1), "https://example.com/\u0000\n");
  } finally {
    if (previousWindow) Object.defineProperty(globalThis, "window", previousWindow);
    else Reflect.deleteProperty(globalThis, "window");
  }
});
