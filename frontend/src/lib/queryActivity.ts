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
  const blur = () => { revision++; nativeActive = false; publish(); };
  const unsubscribe = eventsOn("window-activity", (active: boolean) => {
    revision++;
    nativeActive = active === true;
    publish();
  });
  win.addEventListener("focus", refresh);
  win.addEventListener("blur", blur);
  doc.addEventListener("visibilitychange", refresh);
  publish();
  refresh();
  return () => {
    disposed = true;
    revision++;
    unsubscribe();
    win.removeEventListener("focus", refresh);
    win.removeEventListener("blur", blur);
    doc.removeEventListener("visibilitychange", refresh);
  };
}
