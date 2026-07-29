import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  Download,
  File,
  FileText,
  Image as ImageIcon,
  Music,
  Play,
  Video,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
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
const MAX_ATTACHMENT_CACHE_BYTES = 64 * 1024 * 1024;
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
  const [selectedPdf, setSelectedPdf] = useState<string | null>(null);
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);
  // Index of the video attachment the user clicked play on. null = show thumbnail.
  // Keeping <video> out of the DOM until play-click prevents native controls from
  // intercepting Virtuoso wheel events and stops ResizeObserver cascades.
  const [playingVideoIndex, setPlayingVideoIndex] = useState<number | null>(null);
  const [playingVideoData, setPlayingVideoData] = useState<string | null>(null);
  // Increment to force a re-render when the module-level cache is updated.
  const [, setCacheVersion] = useState(0);
  const bumpCache = (url: string, success: boolean) => {
    console.log(`[Scroll] bumpCache fired: ${success ? "✅" : "❌"} ${url.substring(0, 60)} (msgID: ${messageID})`);
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

    console.log(`[MessageAttachments] Parsed ${parsed.length} attachments from JSON (messageID: ${messageID})`, parsed.map(a => ({ type: a.type, url: a.url, fileName: a.fileName })));

    if (parsed.length === 0) {
      return [];
    }
    
    // Deduplicate attachments by URL (in case backend didn't catch all duplicates)
    const uniqueAttachments: Attachment[] = [];
    const seenAttachmentURLs = new Set<string>();
    let duplicatesRemoved = 0;
    for (const attachment of parsed) {
      if (!seenAttachmentURLs.has(attachment.url)) {
        seenAttachmentURLs.add(attachment.url);
        uniqueAttachments.push(attachment);
      } else {
        duplicatesRemoved++;
        console.log(`[MessageAttachments] Duplicate detected: ${attachment.url.substring(0, 80)}...`);
      }
    }
    if (duplicatesRemoved > 0) {
      console.warn(`[MessageAttachments] ⚠️ Removed ${duplicatesRemoved} duplicate attachments (messageID: ${messageID})`);
    }
    
    console.log(`[MessageAttachments] Returning ${uniqueAttachments.length} unique attachments (messageID: ${messageID})`);
    return uniqueAttachments;
  }, [attachments]);

  // WhatsApp's embedded JPEG thumbnails are often too compressed for the
  // conversation view. Images therefore use their original file; the bounded
  // cache prevents a long conversation from retaining unlimited media.
  // Video, audio and other attachments are still loaded on demand.
  useEffect(() => {
    if (parsedAttachments.length === 0) return;

    const urlsToLoad: Array<{ url: string; fallbackUrl?: string }> = [];

    for (const attachment of parsedAttachments) {
      if (attachment.type === "image" || attachment.mimeType?.startsWith("image/")) {
        const url = attachment.url;
        if (url && !_attachmentDataCache.has(url) && !_attachmentFailedUrls.has(url) && !_attachmentLoadingUrls.has(url)) {
          urlsToLoad.push({ url, fallbackUrl: attachment.thumbnail });
        }
      } else if (attachment.type === "video") {
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
  }, [parsedAttachments]);

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

  const handleImageClick = async (attachment: Attachment) => {
    if (attachment.type === "image" && attachment.url) {
      const url = attachment.url;
      // The conversation view preloads the thumbnail to keep scrolling light.
      // Do not use it here: opening an image must show the original file.
      const cached = getCachedAttachment(url);
      if (cached) {
        setSelectedImage(cached);
        return;
      }
      try {
        const dataUrl = await GetAttachmentData(url);
        if (dataUrl) {
          setSelectedImage(dataUrl);
          return;
        }
      } catch (error) {
        console.error("Failed to load image:", error);
      }

      // Retain a usable preview when the original is no longer available.
      const thumbnail = getCachedAttachment(attachment.thumbnail ?? "");
      if (thumbnail) {
        setSelectedImage(thumbnail);
      }
    }
  };

  const handlePlayVideo = async (attachment: Attachment, index: number) => {
    try {
      // Full video data is intentionally not retained in the shared cache.
      // It is released when playback ends or the message unmounts.
      const dataUrl = await GetAttachmentData(attachment.url);
      setPlayingVideoData(dataUrl);
      setPlayingVideoIndex(index);
    } catch (error) {
      console.error("Failed to load video:", error);
      _attachmentFailedUrls.add(attachment.url);
      bumpCache(attachment.url, false);
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

  return (
    <>
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
          const thumbnail = attachment.thumbnail || attachment.url;
          const audioUrl = getCachedAttachment(attachment.url);
          const videoThumbnailDataUrl = attachment.thumbnail ? getCachedAttachment(attachment.thumbnail) : undefined;
          const imageDataUrl = isImage
            ? getCachedAttachment(attachment.url) ?? getCachedAttachment(attachment.thumbnail ?? "")
            : getCachedAttachment(thumbnail);
          const imageFailed = isImage
            ? _attachmentFailedUrls.has(attachment.url) && _attachmentFailedUrls.has(attachment.thumbnail ?? "")
            : _attachmentFailedUrls.has(thumbnail);

          return (
            <div
              key={`${attachment.url}-${index}`}
              className="relative group flex justify-start"
              onMouseEnter={() => setHoveredIndex(index)}
              onMouseLeave={() => setHoveredIndex(null)}
            >
              {isImage ? (
                // Fixed-size container: skeleton and loaded image share identical dimensions,
                // eliminating the Virtuoso height-remeasure that causes content to jump.
                <div
                  className="message-attachment__image relative cursor-pointer rounded-lg overflow-hidden"
                  style={{ width: "320px", height: "200px", contain: "strict" }}
                  onClick={() => handleImageClick(attachment)}
                >
                  {imageDataUrl ? (
                    <img
                      src={imageDataUrl}
                      alt={attachment.fileName}
                      style={{ width: "100%", height: "100%", objectFit: "contain" }}
                      className="bg-muted"
                    />
                  ) : (
                    <div className="w-full h-full bg-muted flex flex-col items-center justify-center gap-2">
                      <ImageIcon className="h-12 w-12 text-muted-foreground" />
                      {imageFailed && (
                        <span className="text-xs text-muted-foreground">{attachment.fileName}</span>
                      )}
                    </div>
                  )}
                  {hoveredIndex === index && imageDataUrl && (
                    <button
                      className="absolute bottom-2 right-2 p-1.5 rounded-full bg-black/40 hover:bg-black/60 transition-colors"
                      onClick={(e) => { e.stopPropagation(); handleDownload(attachment); }}
                      title="Télécharger"
                    >
                      <Download className="h-4 w-4 text-white" />
                    </button>
                  )}
                </div>
              ) : isVideo ? (
                // contain:strict isolates the container so Virtuoso's ResizeObserver never
                // sees internal dimension changes. The <video> element is only mounted after
                // the user clicks play — keeping it out of the DOM prevents native controls
                // from intercepting Virtuoso wheel events while browsing the conversation.
                <div
                  className="message-attachment__video relative rounded-lg overflow-hidden bg-black"
                  style={{ width: "320px", height: "200px", contain: "strict" }}
                >
                  {playingVideoIndex === index && playingVideoData ? (
                    <video
                      autoPlay
                      controls
                      style={{ width: "100%", height: "100%", objectFit: "contain" }}
                      src={playingVideoData}
                      onEnded={() => { setPlayingVideoIndex(null); setPlayingVideoData(null); }}
                    >
                      <track kind="captions" />
                    </video>
                  ) : (
                    <button
                      className="w-full h-full flex items-center justify-center relative"
                      style={
                        videoThumbnailDataUrl
                          ? { backgroundImage: `url(${videoThumbnailDataUrl})`, backgroundSize: "cover", backgroundPosition: "center" }
                          : undefined
                      }
                      onClick={() => handlePlayVideo(attachment, index)}
                      aria-label={`Lire ${attachment.fileName}`}
                    >
                      {!videoThumbnailDataUrl && <Video className="h-10 w-10 text-white/60" />}
                      <div className="absolute inset-0 flex items-center justify-center">
                        <div className="bg-black/40 rounded-full p-3 hover:bg-black/60 transition-colors">
                          <Play className="h-8 w-8 text-white fill-white" />
                        </div>
                      </div>
                    </button>
                  )}
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
                  {audioUrl ? (
                    <audio
                      controls
                      className="w-full h-8"
                      src={audioUrl}
                    >
                      Your browser does not support the audio element.
                    </audio>
                  ) : (
                    <button className="text-xs opacity-70 text-left underline" onClick={() => GetAttachmentData(attachment.url).then((data) => { cacheAttachment(attachment.url, data); bumpCache(attachment.url, true); }).catch(console.error)}>
                      Load audio
                    </button>
                  )}
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

      <Dialog open={selectedImage !== null} onOpenChange={() => setSelectedImage(null)}>
        <DialogContent className="max-w-4xl max-h-[90vh] p-0">
          <DialogTitle className="sr-only">Image Preview</DialogTitle>
          {selectedImage && (
            <div className="relative">
              <button
                onClick={() => setSelectedImage(null)}
                className="absolute top-2 right-2 z-10 bg-black/50 hover:bg-black/70 text-white rounded-full p-2"
              >
                <X className="h-5 w-5" />
              </button>
              <img
                src={selectedImage}
                alt="Preview"
                className="w-full h-auto max-h-[85vh] object-contain"
              />
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
