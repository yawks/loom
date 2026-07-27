import { useQuery } from "@tanstack/react-query";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { FetchLinkPreview } from "../../wailsjs/go/main/App";
import { cn } from "@/lib/utils";

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

  if (isLoading || isError || !preview) return null;
  if (!preview.title && !preview.imageURL) return null;

  const domain = (() => {
    try { return new URL(preview.url || url).hostname.replace(/^www\./, ""); }
    catch { return ""; }
  })();

  return (
    <button
      type="button"
      onClick={() => BrowserOpenURL(preview.url || url)}
      className={cn(
        "link-preview-card mt-2 w-full max-w-sm rounded-lg overflow-hidden border text-left transition-opacity hover:opacity-80",
        isFromMe
          ? "border-white/20 bg-white/10"
          : "border-border bg-card"
      )}
    >
      {preview.imageURL && (
        <img
          src={preview.imageURL}
          alt={preview.title}
          className="link-preview-card__image w-full object-cover max-h-48"
          onError={(e) => { (e.currentTarget as HTMLImageElement).style.display = "none"; }}
        />
      )}
      <div className="link-preview-card__body p-3 space-y-0.5">
        {domain && (
          <p className={cn("link-preview-card__domain text-xs", isFromMe ? "text-white/60" : "text-muted-foreground")}>
            {domain}
          </p>
        )}
        {preview.title && (
          <p className="link-preview-card__title text-sm font-semibold leading-snug line-clamp-2">
            {preview.title}
          </p>
        )}
        {preview.description && (
          <p className={cn("link-preview-card__description text-xs line-clamp-2", isFromMe ? "text-white/70" : "text-muted-foreground")}>
            {preview.description}
          </p>
        )}
      </div>
    </button>
  );
}
