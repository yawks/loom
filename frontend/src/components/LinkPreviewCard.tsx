import { useQuery } from "@tanstack/react-query";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { FetchLinkPreview } from "../../wailsjs/go/main/App";
import { cn } from "@/lib/utils";
import { Skeleton } from "@/components/ui/skeleton";

interface LinkPreviewCardProps {
  url: string;
  isFromMe?: boolean;
}

export function LinkPreviewCard({ url, isFromMe = false }: LinkPreviewCardProps) {
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
        {preview?.imageURL && !isError ? (
          <img
            src={preview.imageURL}
            alt={title}
            className="link-preview-card__image h-full w-full object-cover"
            onError={(event) => { event.currentTarget.style.visibility = "hidden"; }}
          />
        ) : (
          <div className="link-preview-card__image-placeholder h-full w-full bg-gradient-to-br from-primary/10 to-primary/5" />
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
