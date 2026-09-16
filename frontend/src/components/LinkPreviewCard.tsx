import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  Bug,
  ContactRound,
  CalendarDays,
  Cloud,
  Code2,
  FileText,
  Link as LinkIcon,
  MapPinned,
  Package,
  Presentation,
  Sheet,
  Video,
  type LucideIcon,
} from "lucide-react";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { FetchLinkPreview } from "../../wailsjs/go/main/App";
import { cn } from "@/lib/utils";
import {
  getLinkPreviewFallback,
  type LinkPreviewFallbackBrand,
  type LinkPreviewFallbackType,
} from "@/lib/linkPreviewFallback";

const OFFSCREEN_PREVIEW_DELAY_MS = 1500;

const FALLBACK_ICONS: Record<LinkPreviewFallbackType, LucideIcon> = {
  crm: ContactRound,
  calendar: CalendarDays,
  bugtracker: Bug,
  shopping: Package,
  code: Code2,
  cloud: Cloud,
  video: Video,
  map: MapPinned,
  spreadsheet: Sheet,
  presentation: Presentation,
  document: FileText,
  link: LinkIcon,
};

const FALLBACK_STYLES: Record<LinkPreviewFallbackType, { background: string; badge: string }> = {
  crm: { background: "from-orange-500 via-orange-600 to-red-600", badge: "bg-white/20 text-white" },
  calendar: { background: "from-blue-500 via-blue-600 to-indigo-700", badge: "bg-white/20 text-white" },
  bugtracker: { background: "from-violet-600 via-fuchsia-600 to-pink-500", badge: "bg-white/20 text-white" },
  shopping: { background: "from-orange-400 via-orange-500 to-amber-600", badge: "bg-white/20 text-white" },
  code: { background: "from-slate-700 via-slate-800 to-slate-950", badge: "bg-white/15 text-white" },
  cloud: { background: "from-cyan-500 via-sky-600 to-blue-700", badge: "bg-white/20 text-white" },
  video: { background: "from-red-500 via-rose-600 to-red-700", badge: "bg-white/20 text-white" },
  map: { background: "from-emerald-500 via-teal-600 to-cyan-700", badge: "bg-white/20 text-white" },
  spreadsheet: { background: "from-emerald-600 via-green-700 to-green-900", badge: "bg-white/20 text-white" },
  presentation: { background: "from-orange-500 via-red-600 to-red-800", badge: "bg-white/20 text-white" },
  document: { background: "from-blue-500 via-blue-700 to-indigo-900", badge: "bg-white/20 text-white" },
  link: { background: "from-slate-500 via-slate-600 to-slate-700", badge: "bg-white/20 text-white" },
};

const BRAND_STYLES: Record<LinkPreviewFallbackBrand, { label: string; background: string; badge: string }> = {
  hubspot: { label: "HubSpot · CRM", background: "from-[#ff7a59] via-[#f76845] to-[#d94f2b]", badge: "bg-white text-[#ff7a59]" },
  amazon: { label: "Amazon", background: "from-[#131921] via-[#232f3e] to-[#ff9900]", badge: "bg-[#ff9900] text-[#131921]" },
  youtrack: { label: "YouTrack", background: "from-[#6b57ff] via-[#ff318c] to-[#00b8d9]", badge: "bg-black text-white" },
  jira: { label: "Jira", background: "from-[#0c66e4] via-[#1868db] to-[#579dff]", badge: "bg-white text-[#0c66e4]" },
  github: { label: "GitHub", background: "from-[#0d1117] via-[#161b22] to-[#30363d]", badge: "bg-white text-[#0d1117]" },
  "google-calendar": { label: "Google Calendar", background: "from-[#4285f4] via-[#34a853] to-[#fbbc04]", badge: "bg-white text-[#4285f4]" },
  sharepoint: { label: "SharePoint", background: "from-[#038387] via-[#0078d4] to-[#036c70]", badge: "bg-white text-[#036c70]" },
  youtube: { label: "YouTube", background: "from-[#ff0000] via-red-600 to-red-800", badge: "bg-white text-[#ff0000]" },
};

interface LinkPreviewCardProps {
  url: string;
  isFromMe?: boolean;
}

export function LinkPreviewCard(props: LinkPreviewCardProps) {
  const elementRef = useRef<HTMLDivElement>(null);
  const [isVisible, setIsVisible] = useState(false);
  const hideTimerRef = useRef<number | null>(null);

  useEffect(() => {
    const element = elementRef.current;
    if (!element || typeof IntersectionObserver === "undefined") {
      setIsVisible(true);
      return;
    }
    const observer = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting) {
        if (hideTimerRef.current !== null) {
          window.clearTimeout(hideTimerRef.current);
          hideTimerRef.current = null;
        }
        setIsVisible(true);
        return;
      }

      // Appending a message can briefly move the card outside the observer's
      // intersection during the bottom correction. Do not swap its image for
      // the fallback during that transient layout frame.
      if (hideTimerRef.current === null) {
        hideTimerRef.current = window.setTimeout(() => {
          hideTimerRef.current = null;
          setIsVisible(false);
        }, OFFSCREEN_PREVIEW_DELAY_MS);
      }
    }, {
      rootMargin: "200px 0px",
    });
    observer.observe(element);
    return () => {
      observer.disconnect();
      if (hideTimerRef.current !== null) {
        window.clearTimeout(hideTimerRef.current);
        hideTimerRef.current = null;
      }
    };
  }, []);

  return (
    <div ref={elementRef} className="link-preview-card__visibility-root">
      <LinkPreviewContent {...props} isVisible={isVisible} />
    </div>
  );
}

function LinkPreviewContent({ url, isFromMe = false, isVisible }: LinkPreviewCardProps & { isVisible: boolean }) {
  const { t } = useTranslation();
  const [loadedImageURL, setLoadedImageURL] = useState<string | null>(null);
  const [failedFaviconURL, setFailedFaviconURL] = useState<string | null>(null);
  const [failedImageURL, setFailedImageURL] = useState<string | null>(null);
  const { data: preview, isError } = useQuery({
    queryKey: ["link-preview", url],
    queryFn: () => FetchLinkPreview(url),
    staleTime: 60 * 60 * 1000,
    retry: false,
    enabled: isVisible,
  });

  const domain = (() => {
    try { return new URL(url).hostname.replace(/^www\./, ""); }
    catch { return ""; }
  })();

  // The standard card stays clickable while the thumbnail loads. Both states
  // have identical dimensions to preserve the virtualized message layout.
  // Keep the shared destination and its identity even if metadata fetching
  // redirects to a sign-in page on another domain.
  const targetUrl = url;

  const imageURL = preview?.imageURL;
  const showImage = Boolean(imageURL && !isError && failedImageURL !== imageURL);
  const fallback = getLinkPreviewFallback(targetUrl);
  const title = preview?.title || (fallback.brand ? BRAND_STYLES[fallback.brand].label : domain || url);
  const fallbackType = fallback.type;
  const FallbackIcon = FALLBACK_ICONS[fallbackType];
  const fallbackStyle = fallback.brand ? BRAND_STYLES[fallback.brand] : FALLBACK_STYLES[fallbackType];
  const fallbackLabel = fallback.brand ? BRAND_STYLES[fallback.brand].label : t(`link_preview_fallback.${fallbackType}`);

  return (
    <button
      type="button"
      onClick={() => BrowserOpenURL(targetUrl)}
      className={cn(
        "link-preview-card mt-2 h-60 w-full max-w-sm rounded-lg overflow-hidden border text-left transition-opacity hover:opacity-80",
        isFromMe
          ? "border-white/20 bg-white/10"
          : "border-border bg-card"
      )}
    >
      <div className="link-preview-card__media relative h-36 w-full bg-primary/5">
        {isVisible && showImage ? (
          <img
            src={imageURL}
            alt={title}
            className={cn("link-preview-card__image absolute inset-0 h-full w-full object-cover", loadedImageURL !== imageURL && "opacity-0")}
            onLoad={() => setLoadedImageURL(imageURL || null)}
            onError={() => setFailedImageURL(imageURL || null)}
          />
        ) : null}
        {(!isVisible || !showImage || loadedImageURL !== imageURL) && (
          <div className={cn("link-preview-card__image-placeholder relative flex h-full w-full flex-col items-center justify-center gap-2 overflow-hidden bg-gradient-to-br text-white", fallbackStyle.background)}>
            <div className="absolute -right-8 -top-10 h-28 w-28 rounded-full bg-white/10" aria-hidden="true" />
            <div className="absolute -bottom-12 -left-8 h-32 w-32 rounded-full bg-black/10" aria-hidden="true" />
            <div className={cn("relative flex h-16 w-16 items-center justify-center rounded-2xl shadow-lg ring-1 ring-white/25", fallbackStyle.badge)}>
              {preview?.faviconURL && failedFaviconURL !== preview.faviconURL ? (
                <img src={preview.faviconURL} alt="" className="h-10 w-10 object-contain" onError={() => setFailedFaviconURL(preview.faviconURL)} />
              ) : <FallbackIcon className="h-9 w-9" strokeWidth={1.8} aria-hidden="true" />}
            </div>
            <span className="relative text-sm font-semibold tracking-wide text-white drop-shadow-sm">{preview?.title || fallbackLabel}</span>
          </div>
        )}
      </div>
      <div className="link-preview-card__body h-24 overflow-hidden p-3 space-y-0.5">
        {domain && (
          <p className={cn("link-preview-card__domain flex items-center gap-1.5 text-xs", isFromMe ? "text-white/60" : "text-muted-foreground")}>
            {preview?.faviconURL && failedFaviconURL !== preview.faviconURL && (
              <img src={preview.faviconURL} alt="" className="h-3.5 w-3.5 shrink-0 object-contain" onError={() => setFailedFaviconURL(preview.faviconURL)} />
            )}
            {domain}
          </p>
        )}
        <p className="link-preview-card__title text-sm font-semibold leading-snug line-clamp-2">
          {title}
        </p>
        {preview?.description && (
          <p className={cn("link-preview-card__description text-xs line-clamp-2", isFromMe ? "text-white/70" : "text-muted-foreground")}>
            {preview.description}
          </p>
        )}
      </div>
    </button>
  );
}
