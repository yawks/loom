import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  Bug,
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
import { Skeleton } from "@/components/ui/skeleton";

const FALLBACK_ICONS: Record<LinkPreviewFallbackType, LucideIcon> = {
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

export function LinkPreviewCard({ url, isFromMe = false }: LinkPreviewCardProps) {
  const { t } = useTranslation();
  const [failedImageURL, setFailedImageURL] = useState<string | null>(null);
  const { data: preview, isLoading, isError } = useQuery({
    queryKey: ["link-preview", url],
    queryFn: () => FetchLinkPreview(url),
    staleTime: 60 * 60 * 1000,
    retry: false,
  });

  const domain = (() => {
    try { return new URL(preview?.url || url).hostname.replace(/^www\./, ""); }
    catch { return ""; }
  })();

  // Reserve the final card geometry before metadata and its image arrive. A
  // link preview must never change the measured height of its message.
  if (isLoading) {
    return (
      <div className="link-preview-card link-preview-card__skeleton mt-2 h-60 w-full max-w-sm overflow-hidden rounded-lg border border-border bg-card">
        <Skeleton className="h-36 w-full rounded-none" />
        <div className="h-24 space-y-2 p-3">
          <Skeleton className="h-3 w-24" />
          <Skeleton className="h-4 w-3/4" />
          <Skeleton className="h-3 w-full" />
        </div>
      </div>
    );
  }

  const targetUrl = preview?.url || url;
  const title = preview?.title || domain || url;
  const imageURL = preview?.imageURL;
  const showImage = Boolean(imageURL && !isError && failedImageURL !== imageURL);
  const fallback = getLinkPreviewFallback(targetUrl);
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
      <div className="link-preview-card__media h-36 w-full bg-primary/5">
        {showImage ? (
          <img
            src={imageURL}
            alt={title}
            className="link-preview-card__image h-full w-full object-cover"
            onError={() => setFailedImageURL(imageURL || null)}
          />
        ) : (
          <div className={cn("link-preview-card__image-placeholder relative flex h-full w-full flex-col items-center justify-center gap-2 overflow-hidden bg-gradient-to-br text-white", fallbackStyle.background)}>
            <div className="absolute -right-8 -top-10 h-28 w-28 rounded-full bg-white/10" aria-hidden="true" />
            <div className="absolute -bottom-12 -left-8 h-32 w-32 rounded-full bg-black/10" aria-hidden="true" />
            <div className={cn("relative flex h-16 w-16 items-center justify-center rounded-2xl shadow-lg ring-1 ring-white/25", fallbackStyle.badge)}>
              <FallbackIcon className="h-9 w-9" strokeWidth={1.8} aria-hidden="true" />
            </div>
            <span className="relative text-sm font-semibold tracking-wide text-white drop-shadow-sm">{fallbackLabel}</span>
          </div>
        )}
      </div>
      <div className="link-preview-card__body h-24 overflow-hidden p-3 space-y-0.5">
        {domain && (
          <p className={cn("link-preview-card__domain text-xs", isFromMe ? "text-white/60" : "text-muted-foreground")}>
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
