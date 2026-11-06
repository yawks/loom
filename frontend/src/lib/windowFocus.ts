// Check at the read action itself: React focus state can lag behind native blur.
export function canReadVisibleMessages(): boolean {
  return typeof document !== "undefined" &&
    document.visibilityState === "visible" && document.hasFocus();
}

// WKWebView focus can remain true while another macOS application is active.
export async function confirmReadWindowFocus(): Promise<boolean> {
  if (!canReadVisibleMessages()) return false;
  const app = window.go?.main?.App;
  const check = app?.["IsWindowActive"];
  if (typeof check !== "function") return !app;
  try {
    return await check() === true && canReadVisibleMessages();
  } catch {
    return false;
  }
}
