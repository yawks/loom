import { strict as assert } from "node:assert";
import { test } from "node:test";
import { countStringChars, instrumentBridge } from "../src/lib/bridgeDiagnostics";

test("bridge diagnostics are inactive outside the recording window and preserve promises", async () => {
  const request = Promise.resolve({ avatar: "data:large", name: "name" });
  const original = () => request;
  const bindings = { GetThings: original };
  const diagnostics = instrumentBridge(bindings, () => 1);
  assert.equal(bindings.GetThings(), request);
  await request;
  assert.deepEqual(diagnostics.stop(), []);
  diagnostics.start();
  assert.equal(bindings.GetThings(), request);
  await request;
  diagnostics.event("presence", "{}");
  const result = diagnostics.stop();
  assert.equal(result[0].name, "GetThings");
  assert.equal(result[0].stringChars, 14);
  assert.equal(result[0].calls, 1);
  assert.equal(result[1].name, "event:presence");
  diagnostics.restore();
  assert.equal(bindings.GetThings, original);
});

test("diagnostics ignore late completions from the preceding recording", async () => {
  let resolve!: (value: string) => void;
  const request = new Promise<string>((done) => { resolve = done; });
  const bindings = { GetThings: () => request };
  const diagnostics = instrumentBridge(bindings);
  diagnostics.start();
  bindings.GetThings();
  diagnostics.stop();
  diagnostics.start();
  resolve("old");
  await request;
  assert.deepEqual(diagnostics.stop(), []);
});

test("string counting handles cycles without retaining content", () => {
  const value: any = { text: "abc" };
  value.self = value;
  assert.equal(countStringChars(value), 3);
});
