import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  Download,
  File,
  FileText,
  Image as ImageIcon,
  Music,
  Video,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { GetAttachmentData } from "../../wailsjs/go/main/App";
import { VoiceMessage } from "./VoiceMessage";

// Module-level caches — survive Virtuoso unmount/remount cycles so images don't
// flicker back to the placeholder when the user scrolls away and returns.
const _attachmentDataCache = new Map<string, string>();
const _attachmentFailedUrls = new Set<string>();
const _attachmentLoadingUrls = new Set<string>();

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
}: MessageAttachmentsProps) {
  const [selectedImage, setSelectedImage] = useState<string | null>(null);
  const [selectedPdf, setSelectedPdf] = useState<string | null>(null);
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);
  // Increment to force a re-render when the module-level cache is updated.
  const [, setCacheVersion] = useState(0);
  const bumpCache = () => setCacheVersion((v) => v + 1);

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

    console.log(`[MessageAttachments] Parsed ${parsed.length} attachments from JSON (messageID: ${messageID})`);

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

  if (parsedAttachments.length === 0) {
    return null;
  }
  
  // Load image and audio data URLs.
  // Uses module-level caches so data survives Virtuoso unmount/remount cycles.
  useEffect(() => {
    if (parsedAttachments.length === 0) return;

    const urlsToLoad: string[] = [];

    for (const attachment of parsedAttachments) {
      if (attachment.type === "image" || attachment.type === "video") {
        const url = attachment.thumbnail || attachment.url;
        if (url && !_attachmentDataCache.has(url) && !_attachmentFailedUrls.has(url) && !_attachmentLoadingUrls.has(url)) {
          urlsToLoad.push(url);
        }
      } else if (attachment.type === "audio" || attachment.type === "voice") {
        const url = attachment.url;
        if (url && !_attachmentDataCache.has(url) && !_attachmentFailedUrls.has(url) && !_attachmentLoadingUrls.has(url)) {
          urlsToLoad.push(url);
        }
      }
    }

    if (urlsToLoad.length === 0) return;

    urlsToLoad.forEach((url) => {
      _attachmentLoadingUrls.add(url);
      GetAttachmentData(url)
        .then((dataUrl) => {
          _attachmentDataCache.set(url, dataUrl);
          bumpCache();
        })
        .catch((error) => {
          console.error(`Failed to load attachment ${url}:`, error);
          _attachmentFailedUrls.add(url);
          bumpCache();
        })
        .finally(() => {
          _attachmentLoadingUrls.delete(url);
        });
    });
    // Only depend on parsedAttachments
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [parsedAttachments]);

  const handleDownload = async (attachment: Attachment) => {
    try {
      const dataUrl = await GetAttachmentData(attachment.url);
      const link = document.createElement("a");
      link.href = dataUrl;
      link.download = attachment.fileName;
      document.body.appendChild(link);
      link.click();
      document.body.removeChild(link);
    } catch (error) {
      console.error("Failed to download attachment:", error);
    }
  };

  const handleImageClick = async (attachment: Attachment) => {
    if (attachment.type === "image" && attachment.url) {
      const url = attachment.url;
      // Prefer the full-res URL; fall back to the already-loaded thumbnail.
      const cached = _attachmentDataCache.get(url) ?? _attachmentDataCache.get(attachment.thumbnail ?? "");
      if (cached) {
        setSelectedImage(cached);
        return;
      }
      try {
        const dataUrl = await GetAttachmentData(url);
        if (dataUrl) {
          _attachmentDataCache.set(url, dataUrl);
          setSelectedImage(dataUrl);
        }
      } catch (error) {
        console.error("Failed to load image:", error);
      }
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
          const isImage = attachment.type === "image";
          const isAudio = attachment.type === "audio";
          const isPdf = attachment.mimeType === "application/pdf";
          const thumbnail = attachment.thumbnail || attachment.url;
          const audioUrl = _attachmentDataCache.get(attachment.url);
          const imageDataUrl = _attachmentDataCache.get(thumbnail);
          const imageFailed = _attachmentFailedUrls.has(thumbnail);

          return (
            <div
              key={`${attachment.url}-${index}`}
              className="relative group flex justify-start"
              onMouseEnter={() => setHoveredIndex(index)}
              onMouseLeave={() => setHoveredIndex(null)}
            >
              {isImage ? (
                <div
                  className="relative cursor-pointer rounded-lg overflow-hidden max-w-xs"
                  onClick={() => handleImageClick(attachment)}
                >
                  {thumbnail && imageDataUrl ? (
                    <img
                      src={imageDataUrl}
                      alt={attachment.fileName}
                      className="max-w-full h-auto rounded-lg"
                      style={{ maxHeight: "300px" }}
                    />
                  ) : (
                    <div className="w-48 h-48 bg-muted flex flex-col items-center justify-center rounded-lg gap-1">
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
                      <p className="text-xs opacity-70">
                        {formatFileSize(attachment.fileSize)}
                      </p>
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
                    <div className="text-xs opacity-70">Loading audio...</div>
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
                    <p className="text-xs opacity-70">
                      {formatFileSize(attachment.fileSize)}
                    </p>
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
                    <p className="text-xs opacity-70">
                      {formatFileSize(attachment.fileSize)}
                    </p>
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
