// Check at the read action itself: React focus state can lag behind native blur.
export function canReadVisibleMessages(): boolean {
  return typeof document !== "undefined" &&
    document.visibilityState === "visible" && document.hasFocus();
}
