import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  ChevronLeft,
  ChevronRight,
  Download,
  File,
  FileText,
  Image as ImageIcon,
  Loader2,
  Music,
  Play,
  Video,
  X,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { GetAttachmentData, OpenFile, SaveAttachmentToFile } from "../../wailsjs/go/main/App";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { VoiceMessage } from "./VoiceMessage";

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
}

interface MessageAttachmentsProps {
  attachments: string; // JSON string from message.attachments
  conversationID: string;
  messageID: string;
  isFromMe: boolean;
  layout?: "bubble" | "irc";
  showToast?: (message: string, type?: "error" | "success" | "info", action?: { label: string; onClick: () => void }) => void;
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
}

function getFileIcon(mimeType: string, type: string) {
  if (type === "image" || mimeType.startsWith("image/")) {
    return ImageIcon;
  }
  if (type === "video" || mimeType.startsWith("video/")) {
    return Video;
  }
  if (type === "audio" || mimeType.startsWith("audio/")) {
    return Music;
  }
  if (mimeType === "application/pdf") {
    return FileText;
  }
  if (
    mimeType.includes("excel") ||
    mimeType.includes("spreadsheet") ||
    mimeType.includes("xls")
  ) {
    return FileText;
  }
  return File;
}

function getFileExtension(fileName: string): string {
  const parts = fileName.split(".");
  return parts.length > 1 ? parts[parts.length - 1].toUpperCase() : "";
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
      return;
    }
    if (!attachment.thumbnail) return;
    let active = true;
    GetAttachmentData(attachment.thumbnail)
      .then((data) => { if (active) setThumbnailData(data); })
      .catch(() => undefined);
    return () => { active = false; };
  }, [attachment.thumbnail, isVisible]);

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
}: MessageAttachmentsProps) {
  const { t } = useTranslation();
  const [selectedImage, setSelectedImage] = useState<string | null>(null);
  const [selectedImageIndex, setSelectedImageIndex] = useState<number | null>(null);
  const [selectedPdf, setSelectedPdf] = useState<string | null>(null);
  const [selectedVideo, setSelectedVideo] = useState<string | null>(null);
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);
  const [loadingVideoIndex, setLoadingVideoIndex] = useState<number | null>(null);
  const attachmentsElementRef = useRef<HTMLDivElement>(null);
  const [isVisible, setIsVisible] = useState(false);
  // Increment to force a re-render when the module-level cache is updated.
  const [, setCacheVersion] = useState(0);
  const bumpCache = (_url: string, _success: boolean) => {
    setCacheVersion((v) => v + 1);
  };

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

  const handlePdfClick = async (attachment: Attachment) => {
    if (attachment.mimeType === "application/pdf" && attachment.url) {
      const url = attachment.url;
      try {
        const dataUrl = await GetAttachmentData(url);
        if (dataUrl) {
          setSelectedPdf(dataUrl);
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

          const Icon = getFileIcon(attachment.mimeType, attachment.type);
          const isImage = attachment.type === "image" || attachment.mimeType?.startsWith("image/");
          const isVideo = attachment.type === "video" || attachment.mimeType?.startsWith("video/");
          const isAudio = attachment.type === "audio";
          const isPdf = attachment.mimeType === "application/pdf";
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
                  onOpen={setSelectedImage}
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
                  onClick={() => handlePdfClick(attachment)}
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

      <Dialog open={selectedImage !== null} onOpenChange={() => { setSelectedImage(null); setSelectedImageIndex(null); }}>
        <DialogContent
          className="max-w-4xl max-h-[90vh] p-0"
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
          <DialogTitle className="sr-only">Image Preview</DialogTitle>
          {selectedImage && (
            <div className="relative">
              <button
                onClick={() => { setSelectedImage(null); setSelectedImageIndex(null); }}
                className="absolute top-2 right-2 z-10 bg-black/50 hover:bg-black/70 text-white rounded-full p-2"
              >
                <X className="h-5 w-5" />
              </button>
              <img
                src={selectedImage}
                alt="Preview"
                className="w-full h-auto max-h-[85vh] object-contain"
              />
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

      <Dialog open={selectedPdf !== null} onOpenChange={() => setSelectedPdf(null)}>
        <DialogContent className="max-w-6xl max-h-[90vh] p-0">
          <DialogTitle className="sr-only">PDF Preview</DialogTitle>
          {selectedPdf && (
            <div className="relative w-full h-full">
              <button
                onClick={() => setSelectedPdf(null)}
                className="absolute top-2 right-2 z-10 bg-black/50 hover:bg-black/70 text-white rounded-full p-2"
              >
                <X className="h-5 w-5" />
              </button>
              <iframe
                src={selectedPdf}
                className="w-full h-[85vh] border-0"
                title="PDF Preview"
              />
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
