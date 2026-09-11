// Serialize native changes across navigation, retries and React remounts.
// In particular, a slow disable must never finish after a newer enable.
export function createCaptureProtectionQueue(
  apply: (enabled: boolean) => Promise<void>,
  afterHiddenPaint: () => Promise<void>,
) {
  let pending = Promise.resolve();
  return (enabled: boolean): Promise<void> => {
    const next = pending.then(async () => {
      // Give the compositor a frame with the confidential content removed
      // before making the window capturable again.
      if (!enabled) await afterHiddenPaint();
      await apply(enabled);
    });
    pending = next.catch(() => {});
    return next;
  };
}
