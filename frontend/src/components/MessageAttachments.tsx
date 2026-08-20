import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  ChevronLeft,
  ChevronRight,
  Download,
  Eye,
  File,
  Image as ImageIcon,
  Loader2,
  MapPin,
  Music,
  Phone,
  UserRound,
  Play,
  Video,
  X,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { getDocument, GlobalWorkerOptions, type PDFDocumentProxy } from "pdfjs-dist";
import pdfWorkerUrl from "pdfjs-dist/build/pdf.worker.min.mjs?url";

import { GetAttachmentData, OpenFile, SaveAttachmentToFile } from "../../wailsjs/go/main/App";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { VoiceMessage } from "./VoiceMessage";
import { MessageActions } from "./MessageActions";
import type { MessageHandlers } from "./MessageBubbleItem";
import { getMessageDomId } from "@/lib/messageUtils";
import { models } from "../../wailsjs/go/models";
import { MessageReactions } from "./MessageReactions";

GlobalWorkerOptions.workerSrc = pdfWorkerUrl;

// Module-level cache for small previews. It is deliberately byte-bounded: data
// URLs are substantially larger than their source files and otherwise survive
// for the entire WebView lifetime.
const _attachmentDataCache = new Map<string, string>();
const _attachmentFailedUrls = new Set<string>();
const _attachmentLoadingUrls = new Set<string>();
const MAX_ATTACHMENT_CACHE_BYTES = 32 * 1024 * 1024;
let attachmentCacheBytes = 0;

function getCachedAttachment(url: string): string | undefined {
  const data = _attachmentDataCache.get(url);
  if (data !== undefined) {
    // Map insertion order gives us a compact LRU implementation.
    _attachmentDataCache.delete(url);
    _attachmentDataCache.set(url, data);
  }
  return data;
}

function cacheAttachment(url: string, data: string): void {
  const previous = _attachmentDataCache.get(url);
  if (previous) attachmentCacheBytes -= previous.length * 2;
  _attachmentDataCache.delete(url);

  const bytes = data.length * 2;
  // Never retain a full-resolution file larger than the whole preview cache.
  if (bytes > MAX_ATTACHMENT_CACHE_BYTES) return;
  _attachmentDataCache.set(url, data);
  attachmentCacheBytes += bytes;

  while (attachmentCacheBytes > MAX_ATTACHMENT_CACHE_BYTES) {
    const oldest = _attachmentDataCache.entries().next().value as [string, string] | undefined;
    if (!oldest) break;
    _attachmentDataCache.delete(oldest[0]);
    attachmentCacheBytes -= oldest[1].length * 2;
  }
}

function dataUrlToBytes(dataUrl: string): Uint8Array {
  const comma = dataUrl.indexOf(",");
  if (comma < 0) throw new Error("Invalid attachment data URL");

  const metadata = dataUrl.slice(5, comma);
  const encoded = dataUrl.slice(comma + 1);
  return metadata.includes(";base64")
    ? Uint8Array.from(atob(encoded), (character) => character.charCodeAt(0))
    : new TextEncoder().encode(decodeURIComponent(encoded));
}

// Drop base64 strings before an extended background/sleep period. WebKit can
// retain decoded image surfaces after JS has released the corresponding string.
export function clearAttachmentCache(): void {
  _attachmentDataCache.clear();
  _attachmentLoadingUrls.clear();
  _attachmentFailedUrls.clear();
  attachmentCacheBytes = 0;
}

interface Attachment {
  type: string;
  url: string;
  fileName: string;
  fileSize: number;
  mimeType: string;
  thumbnail?: string;
  latitude?: number;
  longitude?: number;
  locationName?: string;
  address?: string;
  isLive?: boolean;
  accuracy?: number;
  locationUpdatedAt?: string;
  expiresAt?: string;
  contactName?: string;
  contactPhones?: string[];
}

function openStreetMapEmbedURL(latitude: number, longitude: number): string {
  const delta = 0.008;
  const bbox = [longitude - delta, latitude - delta, longitude + delta, latitude + delta]
    .map((coordinate) => coordinate.toFixed(7))
    .join(",");
  return `https://www.openstreetmap.org/export/embed.html?bbox=${encodeURIComponent(bbox)}&layer=mapnik&marker=${latitude.toFixed(7)}%2C${longitude.toFixed(7)}`;
}

interface MessageAttachmentsProps {
  attachments: string; // JSON string from message.attachments
  conversationID: string;
  messageID: string;
  isFromMe: boolean;
  layout?: "bubble" | "irc";
  showToast?: (message: string, type?: "error" | "success" | "info", action?: { label: string; onClick: () => void }) => void;
  galleryMessages?: models.Message[];
  messageHandlers?: MessageHandlers;
  providerInstanceId?: string;
  protocol?: string;
  currentUserId?: string;
  participantNames?: Map<string, string>;
  allMessages?: models.Message[];
  isGroupConversation?: boolean;
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
}

type FileKind = "pdf" | "presentation" | "document" | "spreadsheet" | "other";

type FileBadgeIconProps = React.SVGProps<SVGSVGElement> & {
  accent: string;
  label: string;
  labelSize?: number;
};

function FileBadgeIcon({ accent, label, labelSize = 13, ...props }: FileBadgeIconProps) {
  return (
    <svg viewBox="0 0 32 32" fill="none" aria-hidden="true" {...props}>
      <path d="M7 2.75h11.5L25 9.3v19.95H7z" fill="white" fillOpacity=".96" stroke={accent} strokeWidth="1.5" />
      <path d="M18.5 2.75V9.3H25" fill={accent} fillOpacity=".2" stroke={accent} strokeWidth="1.5" strokeLinejoin="round" />
      <rect x="2" y="10" width="22" height="17" rx="2.5" fill={accent} />
      <text
        x="13"
        y="22.2"
        fill="white"
        fontFamily="Inter, Arial, sans-serif"
        fontSize={labelSize}
        fontWeight="700"
        textAnchor="middle"
      >
        {label}
      </text>
    </svg>
  );
}

const PdfFileIcon = (props: React.SVGProps<SVGSVGElement>) => <FileBadgeIcon {...props} accent="#e53935" label="PDF" labelSize={7.5} />;
const PowerPointFileIcon = (props: React.SVGProps<SVGSVGElement>) => <FileBadgeIcon {...props} accent="#d24726" label="P" />;
const WordFileIcon = (props: React.SVGProps<SVGSVGElement>) => <FileBadgeIcon {...props} accent="#185abd" label="W" />;
const ExcelFileIcon = (props: React.SVGProps<SVGSVGElement>) => <FileBadgeIcon {...props} accent="#107c41" label="X" />;

function getFileKind(fileName: string, mimeType: string): FileKind {
  const extension = getFileExtension(fileName).toLowerCase();

  if (extension === "pdf" || mimeType === "application/pdf") return "pdf";
  if (["ppt", "pptx", "odp"].includes(extension) || mimeType.includes("presentation") || mimeType.includes("powerpoint")) return "presentation";
  if (["doc", "docx", "odt", "rtf"].includes(extension) || mimeType.includes("wordprocessing") || mimeType.includes("msword")) return "document";
  if (["xls", "xlsx", "ods", "csv"].includes(extension) || mimeType.includes("excel") || mimeType.includes("spreadsheet")) return "spreadsheet";
  return "other";
}

function getFileIcon(fileName: string, mimeType: string, type: string) {
  if (type === "image" || mimeType.startsWith("image/")) {
    return ImageIcon;
  }
  if (type === "video" || mimeType.startsWith("video/")) {
    return Video;
  }
  if (type === "audio" || mimeType.startsWith("audio/")) {
    return Music;
  }
  const fileKind = getFileKind(fileName, mimeType);
  if (fileKind === "pdf") return PdfFileIcon;
  if (fileKind === "presentation") return PowerPointFileIcon;
  if (fileKind === "document") return WordFileIcon;
  if (fileKind === "spreadsheet") return ExcelFileIcon;
  return File;
}

function getFileExtension(fileName: string): string {
  const parts = fileName.split(".");
  return parts.length > 1 ? parts[parts.length - 1].toUpperCase() : "";
}

function PdfPreview({
  dataUrl,
  onReady,
  onError,
}: {
  dataUrl: string;
  onReady: () => void;
  onError: (error: unknown) => void;
}) {
  const { t } = useTranslation();
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [document, setDocument] = useState<PDFDocumentProxy | null>(null);
  const [pageNumber, setPageNumber] = useState(1);
  const [pageCount, setPageCount] = useState(0);

  useEffect(() => {
    let active = true;
    const loadingTask = getDocument({ data: dataUrlToBytes(dataUrl) });
    loadingTask.promise
      .then((pdf) => {
        if (!active) {
          void pdf.destroy();
          return;
        }
        setDocument(pdf);
        setPageCount(pdf.numPages);
      })
      .catch((error) => { if (active) onError(error); });

    return () => {
      active = false;
      void loadingTask.destroy();
    };
  }, [dataUrl, onError]);

  useEffect(() => {
    if (!document || !canvasRef.current) return;
    let active = true;
    let renderTask: ReturnType<Awaited<ReturnType<PDFDocumentProxy["getPage"]>>["render"]> | undefined;

    document.getPage(pageNumber)
      .then((page) => {
        if (!active || !canvasRef.current) return;
        const viewport = page.getViewport({ scale: 1.5 });
        const pixelRatio = window.devicePixelRatio || 1;
        const canvas = canvasRef.current;
        const context = canvas.getContext("2d");
        if (!context) throw new Error("Unable to create PDF canvas context");
        canvas.width = Math.floor(viewport.width * pixelRatio);
        canvas.height = Math.floor(viewport.height * pixelRatio);
        canvas.style.width = `${Math.floor(viewport.width)}px`;
        canvas.style.height = `${Math.floor(viewport.height)}px`;
        renderTask = page.render({
          canvas,
          canvasContext: context,
          viewport,
          transform: pixelRatio === 1 ? undefined : [pixelRatio, 0, 0, pixelRatio, 0, 0],
        });
        return renderTask.promise;
      })
      .then(() => { if (active) onReady(); })
      .catch((error) => {
        if (!active) return;
        if (error instanceof Error && error.name === "RenderingCancelledException") return;
        onError(error);
      });

    return () => {
      active = false;
      renderTask?.cancel();
    };
  }, [document, onError, onReady, pageNumber]);

  return (
    <div className="flex h-[85vh] flex-col bg-muted/40">
      {pageCount > 1 && (
        <div className="flex h-12 shrink-0 items-center justify-center gap-3 border-b bg-background px-14">
          <button type="button" onClick={() => setPageNumber((page) => Math.max(1, page - 1))} disabled={pageNumber <= 1} className="rounded p-1 disabled:opacity-40" aria-label={t("previous_page")}>
            <ChevronLeft className="h-5 w-5" />
          </button>
          <span className="text-sm">{t("pdf_page", { current: pageNumber, total: pageCount })}</span>
          <button type="button" onClick={() => setPageNumber((page) => Math.min(pageCount, page + 1))} disabled={pageNumber >= pageCount} className="rounded p-1 disabled:opacity-40" aria-label={t("next_page")}>
            <ChevronRight className="h-5 w-5" />
          </button>
        </div>
      )}
      <div className="flex-1 overflow-auto p-4">
        <canvas ref={canvasRef} className="mx-auto max-w-full bg-white shadow" />
      </div>
    </div>
  );
}

function VisibleImageAttachment({
  attachment,
  onOpen,
  onDownload,
}: {
  attachment: Attachment;
  onOpen: (dataUrl: string) => void;
  onDownload: () => void;
}) {
  const elementRef = useRef<HTMLDivElement>(null);
  const [isVisible, setIsVisible] = useState(false);
  const [imageData, setImageData] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const [loadAttempt, setLoadAttempt] = useState(0);
  const [hovered, setHovered] = useState(false);

  useEffect(() => {
    const element = elementRef.current;
    if (!element || typeof IntersectionObserver === "undefined") {
      setIsVisible(true);
      return;
    }
    const observer = new IntersectionObserver(([entry]) => setIsVisible(entry.isIntersecting), {
      threshold: 0.01,
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (!isVisible) {
      // Remove both the data URL and its decoded image from this DOM subtree.
      // The fixed container keeps Virtuoso's measurements stable while showing
      // the skeleton again outside the viewport.
      setImageData(null);
      setFailed(false);
      return;
    }

    let active = true;
    setFailed(false);
    const load = async () => {
      try {
        const data = await GetAttachmentData(attachment.url);
        if (active) setImageData(data);
      } catch (error) {
        console.warn(`[MessageAttachments] Failed to load image ${attachment.url}:`, error);
        // Preserve accessibility for old messages whose original media is no
        // longer available, but never retain this fallback outside the viewport.
        if (!attachment.thumbnail) {
          if (active) setFailed(true);
          return;
        }
        try {
          const fallback = await GetAttachmentData(attachment.thumbnail);
          if (active) setImageData(fallback);
        } catch (fallbackError) {
          console.warn(`[MessageAttachments] Failed to load image fallback ${attachment.thumbnail}:`, fallbackError);
          if (active) setFailed(true);
        }
      }
    };
    void load();
    return () => { active = false; };
  }, [attachment.thumbnail, attachment.url, isVisible, loadAttempt]);

  return (
    <div
      ref={elementRef}
      className="message-attachment__image relative cursor-pointer rounded-lg overflow-hidden"
      style={{ width: "320px", height: "200px", contain: "strict" }}
      onClick={() => imageData && onOpen(imageData)}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      {imageData ? (
        <img
          src={imageData}
          alt={attachment.fileName}
          style={{ width: "100%", height: "100%", objectFit: "contain" }}
          className="bg-muted"
        />
      ) : (
        <div className="w-full h-full bg-muted flex flex-col items-center justify-center gap-2">
          <ImageIcon className="h-12 w-12 text-muted-foreground" />
          {failed && (
            <>
              <span className="max-w-[90%] truncate text-xs text-muted-foreground">{attachment.fileName}</span>
              <button
                type="button"
                className="rounded bg-background/70 px-2 py-1 text-xs text-foreground hover:bg-background"
                onClick={(event) => {
                  event.stopPropagation();
                  setFailed(false);
                  setLoadAttempt((attempt) => attempt + 1);
                }}
              >
                Réessayer
              </button>
            </>
          )}
        </div>
      )}
      {hovered && imageData && (
        <button
          className="absolute bottom-2 right-2 p-1.5 rounded-full bg-black/40 hover:bg-black/60 transition-colors"
          onClick={(event) => { event.stopPropagation(); onDownload(); }}
          title="Télécharger"
        >
          <Download className="h-4 w-4 text-white" />
        </button>
      )}
    </div>
  );
}

function MosaicImageAttachment({
  attachment,
  onOpen,
  className,
}: {
  attachment: Attachment;
  onOpen: (dataUrl: string) => void;
  className?: string;
}) {
  const elementRef = useRef<HTMLButtonElement>(null);
  const [isVisible, setIsVisible] = useState(false);
  const [imageData, setImageData] = useState<string | null>(null);

  useEffect(() => {
    const element = elementRef.current;
    if (!element || typeof IntersectionObserver === "undefined") {
      setIsVisible(true);
      return;
    }
    const observer = new IntersectionObserver(([entry]) => setIsVisible(entry.isIntersecting), {
      threshold: 0.01,
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (!isVisible) {
      // Release the data URL and its decoded WebKit image surface while the
      // mosaic remains mounted in Virtuoso's overscan area.
      setImageData(null);
      return;
    }

    let active = true;
    GetAttachmentData(attachment.url)
      .then((data) => { if (active) setImageData(data); })
      .catch(() => {
        if (attachment.thumbnail && attachment.thumbnail !== attachment.url) {
          GetAttachmentData(attachment.thumbnail)
            .then((data) => { if (active) setImageData(data); })
            .catch(() => undefined);
        }
    });
    return () => { active = false; };
  }, [attachment.thumbnail, attachment.url, isVisible]);

  return (
    <button
      ref={elementRef}
      type="button"
      className={`message-attachment__mosaic-tile relative min-h-0 overflow-hidden bg-muted ${className || ""}`}
      onClick={() => imageData && onOpen(imageData)}
      aria-label={attachment.fileName}
    >
      {imageData ? (
        <img src={imageData} alt={attachment.fileName} className="h-full w-full object-cover" />
      ) : (
        <ImageIcon className="absolute left-1/2 top-1/2 h-8 w-8 -translate-x-1/2 -translate-y-1/2 text-muted-foreground" />
      )}
    </button>
  );
}

function MosaicVideoAttachment({
  attachment,
  onPlay,
  className,
}: {
  attachment: Attachment;
  onPlay: () => void;
  className?: string;
}) {
  const elementRef = useRef<HTMLButtonElement>(null);
  const [isVisible, setIsVisible] = useState(false);
  const [thumbnailData, setThumbnailData] = useState<string | null>(null);
  const [videoPreviewData, setVideoPreviewData] = useState<string | null>(null);

  useEffect(() => {
    const element = elementRef.current;
    if (!element || typeof IntersectionObserver === "undefined") {
      setIsVisible(true);
      return;
    }
    const observer = new IntersectionObserver(([entry]) => setIsVisible(entry.isIntersecting), { threshold: 0.01 });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (!isVisible) {
      setThumbnailData(null);
      setVideoPreviewData(null);
      return;
    }
    let active = true;
    const loadVideoPreview = () => {
      GetAttachmentData(attachment.url)
        .then((data) => { if (active) setVideoPreviewData(data); })
        .catch(() => undefined);
    };
    if (attachment.thumbnail) {
      GetAttachmentData(attachment.thumbnail)
        .then((data) => { if (active) setThumbnailData(data); })
        .catch(loadVideoPreview);
    } else {
      loadVideoPreview();
    }
    return () => { active = false; };
  }, [attachment.thumbnail, attachment.url, isVisible]);

  return (
    <button
      ref={elementRef}
      type="button"
      className={`message-attachment__mosaic-video relative min-h-0 overflow-hidden bg-black ${className || ""}`}
      onClick={onPlay}
      aria-label={`Lire ${attachment.fileName}`}
    >
      {thumbnailData ? (
        <img src={thumbnailData} alt="" className="h-full w-full object-cover" />
      ) : videoPreviewData ? (
        <video
          src={videoPreviewData}
          muted
          playsInline
          preload="metadata"
          className="h-full w-full object-cover"
          onLoadedMetadata={(event) => {
            const video = event.currentTarget;
            if (Number.isFinite(video.duration) && video.duration > 0) {
              video.currentTime = Math.min(0.25, video.duration / 10);
            }
          }}
        />
      ) : (
        <Video className="absolute left-1/2 top-1/2 h-10 w-10 -translate-x-1/2 -translate-y-1/2 text-white/60" />
      )}
      <span className="absolute left-1/2 top-1/2 flex h-12 w-12 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-black/60 text-white shadow-lg">
        <Play className="h-7 w-7 fill-white" />
      </span>
    </button>
  );
}

export function MessageAttachments({
  attachments,
  conversationID,
  messageID,
  isFromMe,
  layout = "bubble",
  showToast,
  galleryMessages,
  messageHandlers,
  providerInstanceId,
  protocol,
  currentUserId,
  participantNames,
  allMessages,
  isGroupConversation,
}: MessageAttachmentsProps) {
  const { t } = useTranslation();
  const [selectedImage, setSelectedImage] = useState<string | null>(null);
  const [selectedImageIndex, setSelectedImageIndex] = useState<number | null>(null);
  const [selectedPdf, setSelectedPdf] = useState<string | null>(null);
  const [selectedPdfAttachment, setSelectedPdfAttachment] = useState<Attachment | null>(null);
  const [loadingPdfIndex, setLoadingPdfIndex] = useState<number | null>(null);
  const [isPdfFrameLoading, setIsPdfFrameLoading] = useState(false);
  const [pdfPreviewFailed, setPdfPreviewFailed] = useState(false);
  const [selectedVideo, setSelectedVideo] = useState<string | null>(null);
  const [zoomLevel, setZoomLevel] = useState(1);
  const [panOffset, setPanOffset] = useState({ x: 0, y: 0 });
  const [isDragging, setIsDragging] = useState(false);
  const dragRef = useRef<{ startX: number; startY: number; panX: number; panY: number } | null>(null);

  const resetView = () => { setZoomLevel(1); setPanOffset({ x: 0, y: 0 }); };

  const handleZoomIn = () => setZoomLevel((z) => Math.min(4, Math.round((z + 0.25) * 100) / 100));
  const handleZoomOut = () => {
    setZoomLevel((z) => {
      const next = Math.max(0.5, Math.round((z - 0.25) * 100) / 100);
      if (next <= 1) setPanOffset({ x: 0, y: 0 });
      return next;
    });
  };

  const handlePanMouseDown = (e: React.MouseEvent) => {
    if (zoomLevel <= 1 || e.button !== 0) return;
    e.preventDefault();
    dragRef.current = { startX: e.clientX, startY: e.clientY, panX: panOffset.x, panY: panOffset.y };
    setIsDragging(true);
  };

  const handlePanMouseMove = (e: React.MouseEvent) => {
    if (!dragRef.current) return;
    setPanOffset({
      x: dragRef.current.panX + (e.clientX - dragRef.current.startX),
      y: dragRef.current.panY + (e.clientY - dragRef.current.startY),
    });
  };

  const handlePanMouseUp = () => {
    dragRef.current = null;
    setIsDragging(false);
  };
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);
  const [loadingVideoIndex, setLoadingVideoIndex] = useState<number | null>(null);
  const attachmentsElementRef = useRef<HTMLDivElement>(null);
  const [isVisible, setIsVisible] = useState(false);
  // Increment to force a re-render when the module-level cache is updated.
  const [, setCacheVersion] = useState(0);
  const bumpCache = (_url: string, _success: boolean) => {
    setCacheVersion((v) => v + 1);
  };

  const closePdfPreview = () => {
    setSelectedPdf(null);
    setSelectedPdfAttachment(null);
    setIsPdfFrameLoading(false);
    setPdfPreviewFailed(false);
  };

  const handlePdfReady = useCallback(() => {
    setIsPdfFrameLoading(false);
    setPdfPreviewFailed(false);
  }, []);

  const handlePdfPreviewError = useCallback((error: unknown) => {
    console.error("Failed to render PDF preview:", error);
    setIsPdfFrameLoading(false);
    setPdfPreviewFailed(true);
  }, []);

  // Parse and deduplicate attachments using useMemo to avoid re-parsing on every render
  const parsedAttachments = useMemo(() => {
    if (!attachments || attachments.trim() === "") {
      return [];
    }

    let parsed: Attachment[] = [];
    try {
      parsed = JSON.parse(attachments);
    } catch (e) {
      console.error("[MessageAttachments] Failed to parse attachments:", e, "Raw attachments:", attachments);
      return [];
    }

    if (parsed.length === 0) {
      return [];
    }

    const uniqueAttachments: Attachment[] = [];
    const seenAttachmentURLs = new Set<string>();
    for (const attachment of parsed) {
      if (!seenAttachmentURLs.has(attachment.url)) {
        seenAttachmentURLs.add(attachment.url);
        uniqueAttachments.push(attachment);
      }
    }
    return uniqueAttachments;
  }, [attachments]);

  useEffect(() => {
    const element = attachmentsElementRef.current;
    if (!element || typeof IntersectionObserver === "undefined") {
      setIsVisible(true);
      return;
    }
    const observer = new IntersectionObserver(([entry]) => setIsVisible(entry.isIntersecting), {
      rootMargin: "200px 0px",
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  // Videos keep only their compact poster in the shared cache. Images use an
  // IntersectionObserver below: high-resolution data exists only while the
  // corresponding attachment is actually visible.
  useEffect(() => {
    if (!isVisible || parsedAttachments.length === 0) return;

    const urlsToLoad: Array<{ url: string; fallbackUrl?: string }> = [];

    for (const attachment of parsedAttachments) {
      if (attachment.type === "video" || attachment.mimeType?.startsWith("video/")) {
        if (attachment.thumbnail && !_attachmentDataCache.has(attachment.thumbnail) && !_attachmentFailedUrls.has(attachment.thumbnail) && !_attachmentLoadingUrls.has(attachment.thumbnail)) {
          urlsToLoad.push({ url: attachment.thumbnail });
        }
      }
    }

    if (urlsToLoad.length === 0) return;

    urlsToLoad.forEach(({ url, fallbackUrl }) => {
      _attachmentLoadingUrls.add(url);
      GetAttachmentData(url)
        .then((dataUrl) => {
          cacheAttachment(url, dataUrl);
          bumpCache(url, true);
        })
        .catch((error) => {
          console.error(`Failed to load attachment ${url}:`, error);
          _attachmentFailedUrls.add(url);
          if (!fallbackUrl || _attachmentDataCache.has(fallbackUrl) || _attachmentLoadingUrls.has(fallbackUrl)) {
            bumpCache(url, false);
            return;
          }

          // Some older messages no longer have their original media locally.
          // Preserve their existing low-resolution thumbnail as a fallback.
          _attachmentLoadingUrls.add(fallbackUrl);
          GetAttachmentData(fallbackUrl)
            .then((dataUrl) => {
              cacheAttachment(fallbackUrl, dataUrl);
              bumpCache(fallbackUrl, true);
            })
            .catch((fallbackError) => {
              console.error(`Failed to load attachment fallback ${fallbackUrl}:`, fallbackError);
              _attachmentFailedUrls.add(fallbackUrl);
              bumpCache(fallbackUrl, false);
            })
            .finally(() => {
              _attachmentLoadingUrls.delete(fallbackUrl);
            });
        })
        .finally(() => {
          _attachmentLoadingUrls.delete(url);
        });
    });
    // Only depend on parsedAttachments
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isVisible, parsedAttachments]);

  if (parsedAttachments.length === 0) {
    return null;
  }

  const handleDownload = async (attachment: Attachment) => {
    try {
      const savedPath = await SaveAttachmentToFile(attachment.url, attachment.fileName);
      if (!savedPath) return; // user cancelled the dialog
      showToast?.(t("file_saved"), "success", {
        label: t("open_file"),
        onClick: () => OpenFile(savedPath).catch(console.error),
      });
    } catch (error) {
      console.error("Failed to download attachment:", error);
      if (/^https?:\/\//i.test(attachment.url)) {
        try {
          await BrowserOpenURL(attachment.url);
          showToast?.(t("file_opened_in_browser"), "success");
          return;
        } catch (browserError) {
          console.error("Failed to open attachment URL:", browserError);
        }
      }
      showToast?.(t("file_save_error"), "error");
    }
  };

  const handlePlayVideo = async (attachment: Attachment, index: number) => {
    setLoadingVideoIndex(index);
    try {
      const dataUrl = await GetAttachmentData(attachment.url);
      setSelectedVideo(dataUrl);
    } catch (error) {
      console.error("Failed to load video:", error);
      _attachmentFailedUrls.add(attachment.url);
      bumpCache(attachment.url, false);
    } finally {
      setLoadingVideoIndex(null);
    }
  };

  const handlePdfClick = async (attachment: Attachment, index: number) => {
    if (getFileKind(attachment.fileName, attachment.mimeType) === "pdf" && attachment.url && loadingPdfIndex === null) {
      const url = attachment.url;
      setLoadingPdfIndex(index);
      try {
        const dataUrl = await GetAttachmentData(url);
        if (dataUrl) {
          setSelectedPdf(dataUrl);
          setSelectedPdfAttachment(attachment);
          setPdfPreviewFailed(false);
          setIsPdfFrameLoading(true);
        }
      } catch (error) {
        console.error("Failed to load PDF:", error);
        if (/^https?:\/\//i.test(url)) {
          try {
            BrowserOpenURL(url);
          } catch (browserError) {
            console.error("Failed to open PDF URL:", browserError);
          }
        }
      } finally {
        setLoadingPdfIndex(null);
      }
    }
  };

  const imageAttachments = parsedAttachments.filter((attachment) =>
    attachment.type === "image" || attachment.mimeType?.startsWith("image/")
  );
  const visualMediaAttachments = parsedAttachments.filter((attachment) =>
    attachment.type === "image" || attachment.mimeType?.startsWith("image/") ||
    attachment.type === "video" || attachment.mimeType?.startsWith("video/")
  );
  const isMediaMosaic = visualMediaAttachments.length > 1 && visualMediaAttachments.length === parsedAttachments.length;

  const openMosaicImage = (dataUrl: string, index: number) => {
    setSelectedVideo(null);
    setSelectedImage(dataUrl);
    setSelectedImageIndex(index);
  };

  const openMosaicVideo = async (attachment: Attachment, index: number) => {
    setLoadingVideoIndex(index);
    try {
      const data = await GetAttachmentData(attachment.url);
      setSelectedImage(null);
      setSelectedVideo(data);
      setSelectedImageIndex(index);
    } catch (error) {
      console.error("Failed to load gallery video:", error);
    } finally {
      setLoadingVideoIndex(null);
    }
  };

  const navigateMosaic = async (direction: -1 | 1) => {
    if (selectedImageIndex === null || visualMediaAttachments.length < 2) return;
    resetView();
    const nextIndex = (selectedImageIndex + direction + visualMediaAttachments.length) % visualMediaAttachments.length;
    const attachment = visualMediaAttachments[nextIndex];
    try {
      const data = await GetAttachmentData(attachment.url);
      const isVideoAttachment = attachment.type === "video" || attachment.mimeType?.startsWith("video/");
      setSelectedImage(isVideoAttachment ? null : data);
      setSelectedVideo(isVideoAttachment ? data : null);
      setSelectedImageIndex(nextIndex);
    } catch {
      if (!attachment.thumbnail) return;
      try {
        const fallback = await GetAttachmentData(attachment.thumbnail);
        setSelectedVideo(null);
        setSelectedImage(fallback);
        setSelectedImageIndex(nextIndex);
      } catch {
        // Keep the current photo visible when neither source is available.
      }
    }
  };

  const selectedGalleryMessage = selectedImageIndex === null ? undefined : galleryMessages?.[selectedImageIndex];
  const galleryActions = selectedGalleryMessage && messageHandlers ? (
    <div className="message-attachment__gallery-actions absolute left-1/2 top-3 z-30 -translate-x-1/2">
      <MessageActions
        isFromMe={selectedGalleryMessage.isFromMe}
        hasAttachments
        onEdit={() => undefined}
        showEdit={false}
        showDeleteForAll
        onDelete={() => {
          setSelectedImage(null);
          setSelectedVideo(null);
          setSelectedImageIndex(null);
          messageHandlers.onDeleteClick(selectedGalleryMessage);
        }}
        onReply={() => {
          setSelectedImage(null);
          setSelectedVideo(null);
          setSelectedImageIndex(null);
          messageHandlers.onReplyClick(selectedGalleryMessage);
        }}
        onForward={() => {
          messageHandlers.onForwardClick(selectedGalleryMessage, [selectedGalleryMessage]);
        }}
        onReact={(emoji) => messageHandlers.onReaction(selectedGalleryMessage, emoji)}
        currentReactions={(selectedGalleryMessage.reactions || []).filter((reaction) => reaction.userId === currentUserId).map((reaction) => reaction.emoji)}
        messageId={getMessageDomId(selectedGalleryMessage)}
        openActionsMessageId={getMessageDomId(selectedGalleryMessage)}
        provider={protocol}
        instanceId={providerInstanceId}
        className="shadow-xl [&_button]:h-9 [&_button]:w-9"
      />
    </div>
  ) : null;
  const galleryReactions = selectedGalleryMessage?.reactions?.length ? (
    <div className="message-attachment__gallery-reactions absolute left-1/2 top-16 z-30 -translate-x-1/2 rounded-lg bg-black/65 px-2 py-1 shadow-lg backdrop-blur-sm">
      <MessageReactions
        reactions={selectedGalleryMessage.reactions}
        isGroup={isGroupConversation}
        participantNames={participantNames}
        currentUserId={currentUserId}
        providerInstanceId={providerInstanceId}
        allMessages={allMessages}
        onReactionClick={(emoji) => messageHandlers?.onReaction(selectedGalleryMessage, emoji)}
        className="mt-0"
      />
    </div>
  ) : null;

  return (
    <>
      <div ref={attachmentsElementRef} className="message-attachments">
      {isMediaMosaic ? (
        <div
          className="message-attachment__mosaic relative mt-2 grid h-[260px] w-[320px] grid-cols-2 grid-rows-2 gap-0.5 overflow-hidden rounded-lg bg-background/30"
          aria-label={`${visualMediaAttachments.length} médias`}
        >
          {visualMediaAttachments.slice(0, 4).map((attachment, index) => {
            const tileClass = visualMediaAttachments.length === 2 ? "row-span-2" : visualMediaAttachments.length === 3 && index === 0 ? "row-span-2" : "";
            const isVideoAttachment = attachment.type === "video" || attachment.mimeType?.startsWith("video/");
            return isVideoAttachment ? (
              <MosaicVideoAttachment
                key={`${attachment.url}-${index}`}
                attachment={attachment}
                onPlay={() => { void openMosaicVideo(attachment, index); }}
                className={tileClass}
              />
            ) : (
              <MosaicImageAttachment
                key={`${attachment.url}-${index}`}
                attachment={attachment}
                onOpen={(dataUrl) => openMosaicImage(dataUrl, index)}
                className={tileClass}
              />
            );
          })}
          <div className="message-attachment__photo-count pointer-events-none absolute bottom-2 right-2 z-10 rounded-full bg-black/70 px-2.5 py-1 text-xs font-semibold text-white shadow">
            {visualMediaAttachments.length} {imageAttachments.length === visualMediaAttachments.length ? "photos" : "médias"}
          </div>
          {visualMediaAttachments.length > 4 && (
            <div className="message-attachment__overflow-count pointer-events-none absolute bottom-0 right-0 flex h-1/2 w-1/2 items-center justify-center bg-black/55 text-2xl font-semibold text-white">
              +{visualMediaAttachments.length - 3}
            </div>
          )}
        </div>
      ) : (
      <div className="mt-2 space-y-2">
        {parsedAttachments.map((attachment, index) => {
          if (attachment.type === "contact") {
            const name = attachment.contactName?.trim() || t("contact");
            const initials = name
              .split(/\s+/)
              .filter(Boolean)
              .slice(0, 2)
              .map((part) => part[0]?.toLocaleUpperCase())
              .join("");
            return (
              <div
                key={`${attachment.url}-${index}`}
                className="w-[300px] max-w-full overflow-hidden rounded-xl border border-border/70 bg-background/80 shadow-sm"
              >
                <div className="flex items-center gap-3 p-4">
                  <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-emerald-500/15 text-sm font-semibold text-emerald-600 dark:text-emerald-400">
                    {initials || <UserRound className="h-6 w-6" />}
                  </div>
                  <div className="min-w-0">
                    <p className="truncate text-sm font-semibold text-foreground">{name}</p>
                    <p className="mt-0.5 text-xs text-muted-foreground">{t("shared_contact")}</p>
                  </div>
                </div>
                {attachment.contactPhones && attachment.contactPhones.length > 0 && (
                  <div className="border-t border-border/70 px-2 py-2">
                    {attachment.contactPhones.map((phone) => (
                      <button
                        key={phone}
                        type="button"
                        className="flex w-full items-center gap-3 rounded-lg px-2 py-2 text-left text-sm transition-colors hover:bg-muted/70"
                        onClick={() => BrowserOpenURL(`tel:${phone.replace(/[^+\d]/g, "")}`)}
                      >
                        <Phone className="h-4 w-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
                        <span className="min-w-0 flex-1 truncate font-medium">{phone}</span>
                        <span className="text-xs text-muted-foreground">{t("call")}</span>
                      </button>
                    ))}
                  </div>
                )}
              </div>
            );
          }

          if (attachment.type === "location" && attachment.latitude !== undefined && attachment.longitude !== undefined) {
            const liveActive = attachment.isLive && (!attachment.expiresAt || new Date(attachment.expiresAt).getTime() > Date.now());
            return (
              <div key={`${attachment.url}-${index}`} className="w-[320px] max-w-full overflow-hidden rounded-lg border border-border bg-background/60 shadow-sm">
                <iframe
                  title={attachment.locationName || t("location_map")}
                  src={openStreetMapEmbedURL(attachment.latitude, attachment.longitude)}
                  className="h-44 w-full border-0 bg-muted"
                  loading="lazy"
                  referrerPolicy="no-referrer"
                />
                <button
                  type="button"
                  className="flex w-full items-start gap-3 p-3 text-left transition-colors hover:bg-muted/60"
                  onClick={() => BrowserOpenURL(attachment.url)}
                >
                  <MapPin className="mt-0.5 h-5 w-5 shrink-0 text-red-500" />
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-2 text-sm font-medium">
                      {attachment.locationName || t("shared_location")}
                      {attachment.isLive && (
                        <span className={`rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase ${liveActive ? "bg-red-500 text-white" : "bg-muted text-muted-foreground"}`}>
                          {liveActive ? t("live_location") : t("live_location_ended")}
                        </span>
                      )}
                    </span>
                    {attachment.address && <span className="mt-0.5 block text-xs text-muted-foreground">{attachment.address}</span>}
                    <span className="mt-1 block text-[11px] text-muted-foreground">
                      {attachment.latitude.toFixed(5)}, {attachment.longitude.toFixed(5)}
                      {attachment.accuracy ? ` · ±${attachment.accuracy} m` : ""}
                    </span>
                    {attachment.locationUpdatedAt && attachment.isLive && (
                      <span className="mt-1 block text-[11px] text-muted-foreground">
                        {t("location_updated", { time: new Date(attachment.locationUpdatedAt).toLocaleString() })}
                      </span>
                    )}
                    <span className="mt-1 block text-xs font-medium text-primary">OpenStreetMap ↗</span>
                  </span>
                </button>
              </div>
            );
          }

          // Use VoiceMessage for voice messages (type "voice" or audio files that are likely voice messages)
          // Check type "voice" first, then check if it's a small audio file
          if (attachment.type === "voice") {
            return (
              <VoiceMessage
                key={`${attachment.url}-${index}`}
                attachment={{
                  url: attachment.url,
                  duration: (attachment as any).duration,
                  fileName: attachment.fileName
                }}
                conversationID={conversationID}
                messageID={messageID}
                isFromMe={isFromMe}
                layout={layout}
              />
            )
          }
          
          // Also check for small audio files that are likely voice messages
          if (attachment.type === "audio" && attachment.fileSize < 5 * 1024 * 1024) {
            return (
              <VoiceMessage
                key={`${attachment.url}-${index}`}
                attachment={{
                  url: attachment.url,
                  duration: (attachment as any).duration,
                  fileName: attachment.fileName
                }}
                conversationID={conversationID}
                messageID={messageID}
                isFromMe={isFromMe}
                layout={layout}
              />
            )
          }

          const Icon = getFileIcon(attachment.fileName, attachment.mimeType, attachment.type);
          const isImage = attachment.type === "image" || attachment.mimeType?.startsWith("image/");
          const isVideo = attachment.type === "video" || attachment.mimeType?.startsWith("video/");
          const isAudio = attachment.type === "audio";
          const isPdf = getFileKind(attachment.fileName, attachment.mimeType) === "pdf";
          const audioUrl = getCachedAttachment(attachment.url);
          const videoThumbnailDataUrl = isVisible && attachment.thumbnail ? getCachedAttachment(attachment.thumbnail) : undefined;

          return (
            <div
              key={`${attachment.url}-${index}`}
              className="relative group flex justify-start"
              onMouseEnter={() => setHoveredIndex(index)}
              onMouseLeave={() => setHoveredIndex(null)}
            >
              {isImage ? (
                <VisibleImageAttachment
                  attachment={attachment}
                  onOpen={(dataUrl) => {
                    setSelectedImage(dataUrl);
                    if (galleryMessages?.length) setSelectedImageIndex(0);
                  }}
                  onDownload={() => { void handleDownload(attachment); }}
                />
              ) : isVideo ? (
                <div
                  className="message-attachment__video relative rounded-lg overflow-hidden bg-black"
                  style={{ width: "320px", height: "200px", contain: "strict" }}
                >
                  <button
                    className="w-full h-full flex items-center justify-center relative"
                    style={
                      videoThumbnailDataUrl
                        ? { backgroundImage: `url(${videoThumbnailDataUrl})`, backgroundSize: "cover", backgroundPosition: "center" }
                        : undefined
                    }
                    onClick={() => handlePlayVideo(attachment, index)}
                    disabled={loadingVideoIndex === index}
                    aria-label={`Lire ${attachment.fileName}`}
                  >
                    {!videoThumbnailDataUrl && <Video className="h-10 w-10 text-white/60" />}
                    <div className="absolute inset-0 flex items-center justify-center">
                      {loadingVideoIndex === index ? (
                        <div className="bg-black/40 rounded-full p-3">
                          <Loader2 className="h-8 w-8 text-white animate-spin" />
                        </div>
                      ) : (
                        <div className="bg-black/40 rounded-full p-3 hover:bg-black/60 transition-colors">
                          <Play className="h-8 w-8 text-white fill-white" />
                        </div>
                      )}
                    </div>
                  </button>
                  {hoveredIndex === index && (
                    <button
                      className="absolute bottom-2 right-2 p-1.5 rounded-full bg-black/40 hover:bg-black/60 transition-colors"
                      onClick={(e) => { e.stopPropagation(); handleDownload(attachment); }}
                      title="Télécharger"
                    >
                      <Download className="h-4 w-4 text-white" />
                    </button>
                  )}
                </div>
              ) : isAudio ? (
                <div
                  className={`flex flex-col gap-2 p-3 rounded-lg border ${isFromMe && layout === "bubble"
                    ? "bg-blue-600 text-white border-blue-700"
                    : "bg-muted text-foreground border-border"
                    } max-w-xs`}
                >
                  <div className="flex items-center gap-3">
                    <Icon className="h-8 w-8 shrink-0" />
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium truncate">
                        {attachment.fileName || `Audio.${getFileExtension(attachment.fileName)}`}
                      </p>
                      {attachment.fileSize > 0 && (
                        <p className="text-xs opacity-70">{formatFileSize(attachment.fileSize)}</p>
                      )}
                    </div>
                  </div>
                  <div className="message-attachment__audio-player h-8 w-full">
                    {audioUrl ? (
                      <audio
                        controls
                        className="w-full h-8"
                        src={audioUrl}
                      >
                        Your browser does not support the audio element.
                      </audio>
                    ) : (
                      <button className="h-8 w-full rounded bg-primary/10 px-2 text-xs opacity-70 text-left underline" onClick={() => GetAttachmentData(attachment.url).then((data) => { cacheAttachment(attachment.url, data); bumpCache(attachment.url, true); }).catch(console.error)}>
                        Load audio
                      </button>
                    )}
                  </div>
                </div>
              ) : isPdf ? (
                <div
                  className={`flex items-center gap-3 p-3 rounded-lg border ${isFromMe && layout === "bubble"
                    ? "bg-blue-600 text-white border-blue-700"
                    : "bg-muted text-foreground border-border"
                    } max-w-xs cursor-pointer hover:opacity-90 transition-opacity`}
                  onClick={() => { void handlePdfClick(attachment, index); }}
                  aria-busy={loadingPdfIndex === index}
                >
                  <Icon className="h-8 w-8 shrink-0" />
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium truncate">
                      {attachment.fileName || `File.${getFileExtension(attachment.fileName)}`}
                    </p>
                    {attachment.fileSize > 0 && (
                      <p className="text-xs opacity-70">{formatFileSize(attachment.fileSize)}</p>
                    )}
                  </div>
                  <div className={`flex shrink-0 items-center gap-1 transition-opacity ${hoveredIndex === index || loadingPdfIndex === index ? "opacity-100" : "opacity-0"}`}>
                    <button
                      type="button"
                      className="rounded-full p-1.5 hover:bg-black/10 disabled:cursor-wait"
                      onClick={(event) => { event.stopPropagation(); void handlePdfClick(attachment, index); }}
                      disabled={loadingPdfIndex !== null}
                      title="Aperçu du PDF"
                      aria-label="Aperçu du PDF"
                    >
                      {loadingPdfIndex === index ? <Loader2 className="h-5 w-5 animate-spin" /> : <Eye className="h-5 w-5" />}
                    </button>
                    <button
                      type="button"
                      className="rounded-full p-1.5 hover:bg-black/10"
                      onClick={(event) => { event.stopPropagation(); void handleDownload(attachment); }}
                      title="Télécharger"
                      aria-label="Télécharger"
                    >
                      <Download className="h-5 w-5" />
                    </button>
                  </div>
                </div>
              ) : (
                <div
                  className={`flex items-center gap-3 p-3 rounded-lg border ${isFromMe && layout === "bubble"
                    ? "bg-blue-600 text-white border-blue-700"
                    : "bg-muted text-foreground border-border"
                    } max-w-xs cursor-pointer hover:opacity-90 transition-opacity`}
                  onClick={() => handleDownload(attachment)}
                >
                  <Icon className="h-8 w-8 shrink-0" />
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium truncate">
                      {attachment.fileName || `File.${getFileExtension(attachment.fileName)}`}
                    </p>
                    {attachment.fileSize > 0 && (
                      <p className="text-xs opacity-70">{formatFileSize(attachment.fileSize)}</p>
                    )}
                  </div>
                  {hoveredIndex === index && (
                    <Download className="h-5 w-5 shrink-0" />
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>
      )}
      </div>

      <Dialog open={selectedImage !== null} onOpenChange={() => { setSelectedImage(null); setSelectedImageIndex(null); resetView(); }}>
        <DialogContent
          className="max-w-4xl max-h-[90vh] p-0 overflow-hidden"
          onKeyDown={(event) => {
            if (event.key === "ArrowLeft") {
              if (selectedImageIndex === null) return;
              event.preventDefault();
              void navigateMosaic(-1);
            } else if (event.key === "ArrowRight") {
              if (selectedImageIndex === null) return;
              event.preventDefault();
              void navigateMosaic(1);
            } else if (event.key === "+" || event.key === "=") {
              event.preventDefault();
              handleZoomIn();
            } else if (event.key === "-") {
              event.preventDefault();
              handleZoomOut();
            } else if (event.key === "0") {
              event.preventDefault();
              resetView();
            }
          }}
        >
          <DialogTitle className="sr-only">Image Preview</DialogTitle>
          {selectedImage && (
            <div
              className="relative overflow-hidden"
              style={{ cursor: zoomLevel > 1 ? (isDragging ? "grabbing" : "grab") : "default" }}
              onMouseDown={handlePanMouseDown}
              onMouseMove={handlePanMouseMove}
              onMouseUp={handlePanMouseUp}
              onMouseLeave={handlePanMouseUp}
              onWheel={(e) => {
                e.preventDefault();
                const delta = e.deltaY < 0 ? 0.25 : -0.25;
                setZoomLevel((z) => {
                  const next = Math.min(4, Math.max(0.5, Math.round((z + delta) * 100) / 100));
                  if (next <= 1) setPanOffset({ x: 0, y: 0 });
                  return next;
                });
              }}
            >
              {galleryActions}
              {galleryReactions}
              <button
                onClick={() => { setSelectedImage(null); setSelectedImageIndex(null); resetView(); }}
                className="absolute top-2 right-2 z-10 bg-black/50 hover:bg-black/70 text-white rounded-full p-2"
              >
                <X className="h-5 w-5" />
              </button>
              <div className="flex items-center justify-center" style={{ maxHeight: "85vh", overflow: "hidden" }}>
                <img
                  src={selectedImage}
                  alt="Preview"
                  className="w-full h-auto max-h-[85vh] object-contain select-none"
                  style={{
                    transform: `translate(${panOffset.x}px, ${panOffset.y}px) scale(${zoomLevel})`,
                    transformOrigin: "center center",
                    transition: isDragging ? "none" : "transform 0.15s ease",
                  }}
                  draggable={false}
                />
              </div>
              <div className="message-attachment__zoom-controls absolute bottom-3 right-3 z-20 flex gap-1">
                <button
                  type="button"
                  onClick={handleZoomOut}
                  disabled={zoomLevel <= 0.5}
                  className="flex h-9 w-9 items-center justify-center rounded-full bg-black/70 text-white shadow-lg transition hover:bg-black/90 disabled:opacity-40"
                  aria-label="Zoom arrière"
                  title="Zoom arrière (−)"
                >
                  <ZoomOut className="h-5 w-5" />
                </button>
                {(zoomLevel !== 1 || panOffset.x !== 0 || panOffset.y !== 0) && (
                  <button
                    type="button"
                    onClick={resetView}
                    className="flex h-9 min-w-9 items-center justify-center rounded-full bg-black/70 px-2 text-xs font-semibold text-white shadow-lg transition hover:bg-black/90"
                    aria-label="Réinitialiser la vue"
                    title="Réinitialiser la vue (0)"
                  >
                    {Math.round(zoomLevel * 100)}%
                  </button>
                )}
                <button
                  type="button"
                  onClick={handleZoomIn}
                  disabled={zoomLevel >= 4}
                  className="flex h-9 w-9 items-center justify-center rounded-full bg-black/70 text-white shadow-lg transition hover:bg-black/90 disabled:opacity-40"
                  aria-label="Zoom avant"
                  title="Zoom avant (+)"
                >
                  <ZoomIn className="h-5 w-5" />
                </button>
              </div>
              {selectedImageIndex !== null && visualMediaAttachments.length > 1 && (
                <>
                  <button
                    type="button"
                    onClick={() => { void navigateMosaic(-1); }}
                    className="message-attachment__previous-photo absolute left-3 top-1/2 z-20 flex h-14 w-14 -translate-y-1/2 items-center justify-center rounded-full bg-black/70 text-white shadow-lg transition hover:scale-105 hover:bg-black/90"
                    aria-label="Photo précédente"
                    title="Photo précédente"
                  >
                    <ChevronLeft className="h-9 w-9" />
                  </button>
                  <button
                    type="button"
                    onClick={() => { void navigateMosaic(1); }}
                    className="message-attachment__next-photo absolute right-3 top-1/2 z-20 flex h-14 w-14 -translate-y-1/2 items-center justify-center rounded-full bg-black/70 text-white shadow-lg transition hover:scale-105 hover:bg-black/90"
                    aria-label="Photo suivante"
                    title="Photo suivante"
                  >
                    <ChevronRight className="h-9 w-9" />
                  </button>
                  <div className="message-attachment__gallery-position absolute bottom-3 left-1/2 z-20 -translate-x-1/2 rounded-full bg-black/70 px-3 py-1 text-sm font-medium text-white">
                    {selectedImageIndex + 1} / {visualMediaAttachments.length}
                  </div>
                </>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>

      <Dialog open={selectedVideo !== null} onOpenChange={() => { setSelectedVideo(null); setSelectedImageIndex(null); }}>
        <DialogContent
          className="max-w-5xl max-h-[90vh] p-0 bg-black border-0"
          onKeyDown={(event) => {
            if (selectedImageIndex === null) return;
            if (event.key === "ArrowLeft") {
              event.preventDefault();
              void navigateMosaic(-1);
            } else if (event.key === "ArrowRight") {
              event.preventDefault();
              void navigateMosaic(1);
            }
          }}
        >
          <DialogTitle className="sr-only">Video Preview</DialogTitle>
          {selectedVideo && (
            <div className="relative">
              {galleryActions}
              {galleryReactions}
              <button
                onClick={() => { setSelectedVideo(null); setSelectedImageIndex(null); }}
                className="absolute top-2 right-2 z-10 bg-black/50 hover:bg-black/70 text-white rounded-full p-2"
              >
                <X className="h-5 w-5" />
              </button>
              <video
                autoPlay
                controls
                className="w-full max-h-[90vh] object-contain"
                src={selectedVideo}
                onEnded={() => { if (selectedImageIndex === null) setSelectedVideo(null); }}
              >
                <track kind="captions" />
              </video>
              {selectedImageIndex !== null && visualMediaAttachments.length > 1 && (
                <>
                  <button
                    type="button"
                    onClick={() => { void navigateMosaic(-1); }}
                    className="message-attachment__previous-media absolute left-3 top-1/2 z-20 flex h-14 w-14 -translate-y-1/2 items-center justify-center rounded-full bg-black/70 text-white shadow-lg transition hover:scale-105 hover:bg-black/90"
                    aria-label="Média précédent"
                    title="Média précédent"
                  >
                    <ChevronLeft className="h-9 w-9" />
                  </button>
                  <button
                    type="button"
                    onClick={() => { void navigateMosaic(1); }}
                    className="message-attachment__next-media absolute right-3 top-1/2 z-20 flex h-14 w-14 -translate-y-1/2 items-center justify-center rounded-full bg-black/70 text-white shadow-lg transition hover:scale-105 hover:bg-black/90"
                    aria-label="Média suivant"
                    title="Média suivant"
                  >
                    <ChevronRight className="h-9 w-9" />
                  </button>
                  <div className="message-attachment__gallery-position absolute bottom-3 left-1/2 z-20 -translate-x-1/2 rounded-full bg-black/70 px-3 py-1 text-sm font-medium text-white">
                    {selectedImageIndex + 1} / {visualMediaAttachments.length}
                  </div>
                </>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>

      <Dialog open={selectedPdf !== null} onOpenChange={(open) => { if (!open) closePdfPreview(); }}>
        <DialogContent className="max-w-6xl max-h-[90vh] p-0">
          <DialogTitle className="sr-only">PDF Preview</DialogTitle>
          {selectedPdf && (
            <div className="relative w-full h-full">
              <button
                onClick={closePdfPreview}
                className="absolute top-2 right-2 z-10 bg-black/50 hover:bg-black/70 text-white rounded-full p-2"
              >
                <X className="h-5 w-5" />
              </button>
              {isPdfFrameLoading && (
                <div className="absolute inset-0 z-[5] flex items-center justify-center bg-background/80" role="status" aria-label={t("loading")}>
                  <Loader2 className="h-10 w-10 animate-spin text-muted-foreground" />
                </div>
              )}
              {pdfPreviewFailed && (
                <div className="absolute inset-0 z-[6] flex flex-col items-center justify-center gap-4 bg-background p-6 text-center">
                  <p className="text-sm text-muted-foreground">{t("pdf_preview_error")}</p>
                  {selectedPdfAttachment && (
                    <button
                      type="button"
                      className="rounded-md bg-primary px-4 py-2 text-sm text-primary-foreground"
                      onClick={() => { void handleDownload(selectedPdfAttachment); }}
                    >
                      {t("download")}
                    </button>
                  )}
                </div>
              )}
              {!pdfPreviewFailed && (
                <PdfPreview dataUrl={selectedPdf} onReady={handlePdfReady} onError={handlePdfPreviewError} />
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
