export type BridgeMetric = {
  name: string;
  calls: number;
  errors: number;
  stringChars: number;
  maxStringChars: number;
  elapsedMs: number;
};

type Binding = (...args: any[]) => Promise<any>;
type Bindings = Record<string, Binding>;

// Count string data, not JSON bytes. In particular this identifies repeated
// inline images without encoding another large JSON document during profiling.
export function countStringChars(value: unknown, seen = new WeakSet<object>()): number {
  if (typeof value === "string") return value.length;
  if (!value || typeof value !== "object" || seen.has(value)) return 0;
  seen.add(value);
  return Object.values(value).reduce<number>((sum, item) => sum + countStringChars(item, seen), 0);
}

export function instrumentBridge(bindings: Bindings, now = () => performance.now()) {
  const metrics = new Map<string, BridgeMetric>();
  const originals = new Map<string, Binding>();
  let active = false;
  let generation = 0;
  const record = (name: string, value: unknown, elapsedMs = 0, failed = false) => {
    if (!active) return;
    const chars = countStringChars(value);
    const metric = metrics.get(name) ?? { name, calls: 0, errors: 0, stringChars: 0, maxStringChars: 0, elapsedMs: 0 };
    metric.calls++;
    metric.errors += Number(failed);
    metric.stringChars += chars;
    metric.maxStringChars = Math.max(metric.maxStringChars, chars);
    metric.elapsedMs += elapsedMs;
    metrics.set(name, metric);
  };
  for (const [name, original] of Object.entries(bindings)) {
    if (typeof original !== "function" || name === "ReportBridgeDiagnostics") continue;
    originals.set(name, original);
    bindings[name] = function (...args) {
      if (!active) return original.apply(this, args);
      const started = now();
      const current = generation;
      const request = original.apply(this, args);
      // Preserve the exact Wails promise (including its cancellation API).
      void request.then(
        (value) => { if (current === generation) record(name, value, now() - started); },
        () => { if (current === generation) record(name, undefined, now() - started, true); },
      );
      return request;
    };
  }
  return {
    start() { metrics.clear(); generation++; active = true; },
    event(name: string, value: unknown) { record(`event:${name}`, value); },
    stop() { active = false; generation++; return [...metrics.values()].sort((a, b) => b.stringChars - a.stringChars); },
    restore() { active = false; for (const [name, original] of originals) bindings[name] = original; },
  };
}

export async function setupBridgeDiagnostics(eventsOn: (name: string, callback: (...args: any[]) => void) => (() => void)) {
  const bindings = (window as unknown as { go?: { main?: { App?: Bindings } } }).go?.main?.App;
  if (!bindings?.GetBridgeDiagnosticsEnabled || !await bindings.GetBridgeDiagnosticsEnabled()) return;
  const diagnostics = instrumentBridge(bindings);
  const events = ["new-message", "new-messages-batch", "contacts-refresh", "contact-status", "presence", "typing", "receipt", "reaction", "reactions-batch", "sync-status"];
  for (const name of events) eventsOn(name, (...args) => diagnostics.event(name, args));
  eventsOn("diagnostics:bridge", (active: boolean) => {
    if (active) diagnostics.start();
    else void bindings.ReportBridgeDiagnostics(JSON.stringify(diagnostics.stop()));
  });
}
