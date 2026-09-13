// Serialize native changes across navigation, retries and React remounts.
// In particular, a slow disable must never finish after a newer enable.
export function createCaptureProtectionQueue(
  apply: (enabled: boolean) => Promise<void>,
  afterHiddenPaint: () => Promise<void>,
  attempts = 1,
  retryDelay: () => Promise<void> = () => new Promise((resolve) => setTimeout(resolve, 200)),
) {
  let pending = Promise.resolve();
  return (enabled: boolean): Promise<void> => {
    const next = pending.then(async () => {
      // Give the compositor a frame with the confidential content removed
      // before making the window capturable again.
      if (!enabled) await afterHiddenPaint();
      for (let attempt = 0; ; attempt++) {
        try {
          await apply(enabled);
          break;
        } catch (error) {
          if (attempt + 1 >= attempts) throw error;
          await retryDelay();
        }
      }
    });
    pending = next.catch(() => {});
    return next;
  };
}
