import { strict as assert } from "node:assert";
import { test } from "node:test";
import { watchQueryActivity } from "../src/lib/queryActivity";

test("native activity overrides stale browser focus and late checks", async () => {
  const win = new EventTarget();
  const doc = Object.assign(new EventTarget(), { visibilityState: "visible", hasFocus: () => true });
  let notify!: (active: boolean) => void;
  let resolve!: (active: boolean) => void;
  const states: boolean[] = [];
  const stop = watchQueryActivity((active) => states.push(active), (_name, callback) => {
    notify = callback;
    return () => {};
  }, () => new Promise<boolean>((done) => { resolve = done; }), doc as Document, win as Window);
  notify(false);
  assert.equal(states.at(-1), false);
  resolve(true);
  await Promise.resolve();
  assert.equal(states.at(-1), false);
  notify(true);
  assert.equal(states.at(-1), true);
  doc.visibilityState = "hidden";
  doc.dispatchEvent(new Event("visibilitychange"));
  assert.equal(states.at(-1), false);
  notify(true);
  assert.equal(states.at(-1), false);
  stop();
  const count = states.length;
  win.dispatchEvent(new Event("focus"));
  assert.equal(states.length, count);
});

test("browser activity works without a native implementation", () => {
  const win = new EventTarget();
  let hasFocus = true;
  const doc = Object.assign(new EventTarget(), { visibilityState: "visible", hasFocus: () => hasFocus });
  let active = false;
  const stop = watchQueryActivity((value) => { active = value; }, () => () => {}, undefined, doc as Document, win as Window);
  assert.equal(active, true);
  win.dispatchEvent(new Event("blur"));
  assert.equal(active, false);
  hasFocus = true;
  win.dispatchEvent(new Event("focus"));
  assert.equal(active, true);
  stop();
});

test("a spurious WebView blur does not strand an active native window", async () => {
  const win = new EventTarget();
  const doc = Object.assign(new EventTarget(), { visibilityState: "visible", hasFocus: () => true });
  let active = false;
  let nativeActive = true;
  const stop = watchQueryActivity((value) => { active = value; }, () => () => {},
    async () => nativeActive, doc as Document, win as Window);
  await Promise.resolve();
  assert.equal(active, true);
  win.dispatchEvent(new Event("blur"));
  await Promise.resolve();
  assert.equal(active, true);
  nativeActive = false;
  win.dispatchEvent(new Event("blur"));
  await Promise.resolve();
  assert.equal(active, false);
  stop();
});

test("a click reconciles a missed native reactivation", async () => {
  const win = new EventTarget();
  const doc = Object.assign(new EventTarget(), { visibilityState: "visible", hasFocus: () => true });
  let notify!: (active: boolean) => void;
  let active = false;
  let nativeActive = false;
  const stop = watchQueryActivity((value) => { active = value; }, (_name, callback) => {
    notify = callback;
    return () => {};
  }, async () => nativeActive, doc as Document, win as Window);
  await Promise.resolve();
  assert.equal(active, false);
  nativeActive = true;
  win.dispatchEvent(new Event("pointerdown"));
  await Promise.resolve();
  assert.equal(active, true);
  notify(false);
  nativeActive = false;
  win.dispatchEvent(new Event("pointerdown"));
  await Promise.resolve();
  assert.equal(active, false);
  stop();
});
