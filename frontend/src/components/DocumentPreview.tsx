import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronLeft, ChevronRight, Loader2, ZoomIn, ZoomOut } from "lucide-react";
import { GetAttachmentData, GetProviderAttachmentData } from "../../wailsjs/go/main/App";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { dataUrlToBytes } from "@/lib/attachmentData";
import type { OfficeFormat } from "@/lib/officeDocument";
import type { HyperlinkTarget } from "@silurus/ooxml/xlsx";

type Viewer = {
  load(source: ArrayBuffer): Promise<void>;
  destroy(): void;
  zoomIn(): Promise<void> | void;
  zoomOut(): Promise<void> | void;
};

export default function DocumentPreview({ url, format, providerInstanceId }: {
  url: string;
  format: OfficeFormat;
  providerInstanceId?: string;
}) {
  const { t } = useTranslation();
  const hostRef = useRef<HTMLDivElement>(null);
  const viewerRef = useRef<Viewer | null>(null);
  const navigateRef = useRef<((index: number) => Promise<void>) | null>(null);
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [page, setPage] = useState({ index: 0, total: 0 });

  useEffect(() => {
    const host = hostRef.current!;
    // A separate mount keeps an obsolete async load away from the next viewer.
    const mount = document.createElement("div");
    mount.style.cssText = "height:100%;width:100%;overflow:auto;position:relative";
    host.appendChild(mount);
    let disposed = false;
    let viewer: Viewer | undefined;
    const fail = () => { if (!disposed) setStatus("error"); };
    const changed = (index: number, total: number) => {
      if (!disposed) setPage({ index, total });
    };
    const onHyperlinkClick = (target: HyperlinkTarget) => {
      if (!disposed && target.kind === "external" && /^(https?:|mailto:)/i.test(target.url)) {
        void BrowserOpenURL(target.url);
      }
    };
    const options = { mode: "worker" as const, useGoogleFonts: false, onError: fail, onHyperlinkClick };
    const load = async () => {
      const data = providerInstanceId
        ? await GetProviderAttachmentData(providerInstanceId, url)
        : await GetAttachmentData(url);
      if (disposed) return;
      const bytes = dataUrlToBytes(data).buffer;
      if (disposed) return;
      if (format === "xlsx") {
        const { XlsxViewer } = await import("@silurus/ooxml/xlsx");
        if (disposed) return;
        viewer = new XlsxViewer(mount, { ...options, resizable: false, showZoomSlider: false });
      } else {
        // Auto margins center the complete viewer (canvas and text/link layers).
        // When zoomed wider than the viewport, margins collapse to zero so the
        // left edge remains reachable through horizontal scrolling.
        mount.style.display = "flex";
        mount.style.flexDirection = "column";
        const canvas = document.createElement("canvas");
        mount.appendChild(canvas);
        if (format === "docx") {
          const { DocxViewer } = await import("@silurus/ooxml/docx");
          if (disposed) return;
          const doc = new DocxViewer(canvas, { ...options, container: mount, onPageChange: changed });
          viewer = doc;
          navigateRef.current = (index) => doc.goToPage(index);
        } else {
          const { PptxViewer } = await import("@silurus/ooxml/pptx");
          if (disposed) return;
          const slides = new PptxViewer(canvas, { ...options, width: mount.clientWidth, onSlideChange: changed });
          viewer = slides;
          navigateRef.current = (index) => slides.goToSlide(index);
        }
      }
      if (format !== "xlsx") {
        const page = mount.firstElementChild as HTMLElement | null;
        if (page) {
          page.style.marginInline = "auto";
          page.style.flexShrink = "0";
          page.style.alignSelf = "flex-start";
        }
      }
      viewerRef.current = viewer;
      await viewer.load(bytes);
      if (!disposed) setStatus((current) => current === "error" ? current : "ready");
    };
    void load().catch(fail);
    return () => {
      disposed = true;
      viewerRef.current = null;
      navigateRef.current = null;
      viewer?.destroy();
      mount.remove();
    };
  }, [url, format, providerInstanceId]);

  const run = (action: Promise<void> | void) => { void Promise.resolve(action).catch(() => setStatus("error")); };
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      <div className="flex items-center justify-center gap-3">
        {format !== "xlsx" && <>
          <button aria-label={t("office_previous")} disabled={status !== "ready" || page.index === 0} onClick={() => run(navigateRef.current?.(page.index - 1))}><ChevronLeft /></button>
          <span aria-live="polite">{page.total ? `${page.index + 1} / ${page.total}` : "—"}</span>
          <button aria-label={t("office_next")} disabled={status !== "ready" || page.index + 1 >= page.total} onClick={() => run(navigateRef.current?.(page.index + 1))}><ChevronRight /></button>
        </>}
        <button aria-label={t("office_zoom_out")} disabled={status !== "ready"} onClick={() => run(viewerRef.current?.zoomOut())}><ZoomOut /></button>
        <button aria-label={t("office_zoom_in")} disabled={status !== "ready"} onClick={() => run(viewerRef.current?.zoomIn())}><ZoomIn /></button>
      </div>
      {format === "xlsx" && <p className="text-xs text-muted-foreground">{t("office_formula_note")}</p>}
      <div className="relative min-h-0 flex-1 overflow-hidden rounded border bg-white text-black">
        <div ref={hostRef} className="h-full w-full" />
        {status === "loading" && <div className="absolute inset-0 flex items-center justify-center bg-background/80" role="status" aria-label={t("loading")}><Loader2 className="animate-spin" /></div>}
        {status === "error" && <div className="absolute inset-0 flex items-center justify-center bg-background p-6 text-center text-foreground" role="alert">{t("office_preview_error")}</div>}
      </div>
    </div>
  );
}
