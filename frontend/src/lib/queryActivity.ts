type Events = (name: string, callback: (...args: any[]) => void) => () => void;
type Visibility = Pick<Document, "visibilityState" | "hasFocus" | "addEventListener" | "removeEventListener">;
type WindowEvents = Pick<Window, "addEventListener" | "removeEventListener">;

// Install as TanStack Query's focus listener. Browser events handle all
// platforms; a native event corrects stale WKWebView focus without any timer.
export function watchQueryActivity(
  focused: (active: boolean) => void,
  eventsOn: Events,
  checkNative?: () => Promise<boolean>,
  doc: Visibility = document,
  win: WindowEvents = window,
) {
  let disposed = false;
  let revision = 0;
  let nativeActive: boolean | undefined;
  const publish = () => focused(doc.visibilityState === "visible" && doc.hasFocus() && nativeActive !== false);
  const refresh = () => {
    const current = ++revision;
    if (doc.visibilityState !== "visible") { focused(false); return; }
    if (!checkNative) { nativeActive = undefined; publish(); return; }
    void checkNative().then((active) => {
      if (!disposed && current === revision) { nativeActive = active; publish(); }
    }).catch(() => { if (!disposed && current === revision) { nativeActive = undefined; publish(); } });
  };
  const blur = () => {
    // A WebView can dispatch blur during an internal focus transition while
    // the native window remains active. Treating it as a native deactivation
    // leaves activity false forever if no subsequent window focus event fires.
    if (checkNative) refresh();
    else { revision++; nativeActive = false; publish(); }
  };
  // A click in an already-active window is also an opportunity to reconcile a
  // stale native deactivation when WebKit did not dispatch another focus event.
  const pointerDown = () => {
    if (nativeActive === false && doc.visibilityState === "visible") refresh();
  };
  const unsubscribe = eventsOn("window-activity", (active: boolean) => {
    revision++;
    nativeActive = active === true;
    publish();
  });
  win.addEventListener("focus", refresh);
  win.addEventListener("blur", blur);
  win.addEventListener("pointerdown", pointerDown);
  doc.addEventListener("visibilitychange", refresh);
  publish();
  refresh();
  return () => {
    disposed = true;
    revision++;
    unsubscribe();
    win.removeEventListener("focus", refresh);
    win.removeEventListener("blur", blur);
    win.removeEventListener("pointerdown", pointerDown);
    doc.removeEventListener("visibilitychange", refresh);
  };
}
