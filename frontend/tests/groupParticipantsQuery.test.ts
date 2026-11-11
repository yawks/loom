import { strict as assert } from "node:assert";
import { test } from "node:test";
import { QueryClient } from "@tanstack/react-query";
import { groupParticipantsQuery } from "../src/lib/groupParticipantsQuery";

test("timeline and sidebar share membership fetches and unchanged data", async (t) => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, "window");
  let calls = 0;
  let resolve!: (items: any[]) => void;
  const pending = new Promise<any[]>((done) => { resolve = done; });
  Object.defineProperty(globalThis, "window", { configurable: true, value: {
    go: { main: { App: { GetGroupParticipants: () => { calls++; return pending; } } } },
  } });
  t.after(() => {
    if (previous) Object.defineProperty(globalThis, "window", previous);
    else Reflect.deleteProperty(globalThis, "window");
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  t.after(() => client.clear());
  const options = groupParticipantsQuery("account::conversation", true);
  const timeline = client.fetchQuery(options);
  const sidebar = client.fetchQuery(options);
  assert.equal(calls, 1);
  resolve([{ userId: "one", displayName: "One" }]);
  assert.deepEqual(await timeline, await sidebar);
  const initial = client.getQueryData(options.queryKey);
  await client.fetchQuery(options);
  assert.equal(calls, 1);
  await client.fetchQuery({ ...options, staleTime: 0, queryFn: async () => [{ userId: "one", displayName: "One" }] as any });
  assert.equal(client.getQueryData(options.queryKey), initial);
  const direct = await client.fetchQuery(groupParticipantsQuery("account::direct", false));
  assert.deepEqual(direct, []);
  assert.equal(calls, 1);
});
