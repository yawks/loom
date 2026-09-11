import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createCaptureProtectionQueue } from "../src/lib/captureProtectionQueue.ts";

test("rapid navigation waits for hiding and serializes a slow disable before enabling", async () => {
  const events: string[] = [];
  let finishDisable!: () => void;
  const disableFinished = new Promise<void>((resolve) => { finishDisable = resolve; });
  let disableStarted!: () => void;
  const started = new Promise<void>((resolve) => { disableStarted = resolve; });
  const queue = createCaptureProtectionQueue(async (enabled) => {
    events.push(`start:${enabled}`);
    if (!enabled) { disableStarted(); await disableFinished; }
    events.push(`end:${enabled}`);
  }, async () => { events.push("hidden paint"); });
  const disable = queue(false);
  const enable = queue(true);
  await started;
  assert.deepEqual(events, ["hidden paint", "start:false"]);
  finishDisable();
  await Promise.all([disable, enable]);
  assert.deepEqual(events, ["hidden paint", "start:false", "end:false", "start:true", "end:true"]);
});

test("native failures reach the caller and do not prevent a retry", async () => {
  let attempts = 0;
  const queue = createCaptureProtectionQueue(async () => {
    if (++attempts === 1) throw new Error("native failure");
  }, async () => {});
  await assert.rejects(queue(true), /native failure/);
  await queue(true);
  assert.equal(attempts, 2);
});

test("failed hiding never disables native protection", async () => {
  let called = false;
  const queue = createCaptureProtectionQueue(async () => { called = true; }, async () => {
    throw new Error("paint failed");
  });
  await assert.rejects(queue(false), /paint failed/);
  assert.equal(called, false);
});
