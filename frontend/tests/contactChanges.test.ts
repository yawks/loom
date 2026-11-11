import { test } from "node:test";
import assert from "node:assert/strict";
import { createContactLoader, createCompatibleContactLoader } from "../src/lib/contactChanges";
import type { ContactChanges } from "../src/lib/contactChanges";
import type { models } from "../wailsjs/go/models";

const contact = (id: number, displayName = `${id}`) => ({ id, displayName }) as models.MetaContact;

test("older backends use the full snapshot API without invoking unavailable bindings", async () => {
  let available = false;
  const load = createCompatibleContactLoader(
    async revision => {
      assert.equal(revision, "");
      return { revision: "a", reset: true, contacts: [contact(2)] };
    },
    async () => [contact(1)],
    () => available,
  );
  assert.deepEqual(await load(), [contact(1)]);
  available = true;
  assert.deepEqual(await load(), [contact(2)]);
});

test("contact deltas preserve unchanged references and handle removals/resets", async () => {
  const one = contact(1), two = contact(2), changed = contact(2, "changed"), three = contact(3);
  const responses: ContactChanges[] = [
    { revision: "a", reset: true, contacts: [one, two] },
    { revision: "a", reset: false },
    { revision: "b", reset: false, contacts: [changed, three], removedIds: [1] },
    { revision: "c", reset: true },
  ];
  const revisions: string[] = [];
  const load = createContactLoader(async revision => { revisions.push(revision); return responses.shift()!; });
  const initial = await load();
  assert.equal(await load(), initial);
  const delta = await load();
  assert.deepEqual(delta, [changed, three]);
  assert.equal(delta[0], changed);
  assert.deepEqual(await load(), []);
  assert.deepEqual(revisions, ["", "a", "a", "b"]);
});

test("concurrent loads coalesce and failures do not advance revision", async () => {
  let calls = 0;
  let finish!: (value: ContactChanges) => void;
  const revisions: string[] = [];
  const load = createContactLoader(revision => {
    revisions.push(revision);
    calls++;
    if (calls === 1) return new Promise(resolve => { finish = resolve; });
    if (calls === 2) return Promise.reject(new Error("offline"));
    return Promise.resolve({ revision: "b", reset: false, contacts: [contact(2)] });
  });
  const first = load();
  assert.equal(load(), first);
  finish({ revision: "a", reset: true, contacts: [contact(1)] });
  const initial = await first;
  await assert.rejects(load(), /offline/);
  const next = await load();
  assert.equal(next[0], initial[0]);
  assert.deepEqual(revisions, ["", "a", "a"]);
});
