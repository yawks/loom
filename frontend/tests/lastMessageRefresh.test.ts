import { strict as assert } from "node:assert";
import { test } from "node:test";
import { mergeInFlightLastMessages } from "../src/lib/lastMessageRefresh.ts";

test("a live message arriving during a summary fetch is not overwritten", () => {
  const old = { timestamp: "2026-11-13T10:19:00Z", id: "old" };
  const live = { timestamp: "2026-11-13T14:15:00Z", id: "live" };
  assert.equal(mergeInFlightLastMessages({ c: old }, { c: live }, { c: old }).c, live);
});

test("a newer database result wins and unchanged entries can be removed", () => {
  const old = { timestamp: "2026-11-13T10:19:00Z", id: "old" };
  const live = { timestamp: "2026-11-13T11:00:00Z", id: "live" };
  const latest = { timestamp: "2026-11-13T14:15:00Z", id: "latest" };
  const merged = mergeInFlightLastMessages({ c: old, removed: old }, { c: live, removed: old }, { c: latest });
  assert.equal(merged.c, latest);
  assert.equal(merged.removed, undefined);
});
