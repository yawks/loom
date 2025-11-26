import { useState, useEffect } from "react";
import {
  FileText,
  Image as ImageIcon,
  Video,
  Music,
  File,
  Download,
  X,
} from "lucide-react";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { GetAttachmentData } from "../../wailsjs/go/main/App";

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
  isFromMe: boolean;
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
  isFromMe,
}: MessageAttachmentsProps) {
  const [selectedImage, setSelectedImage] = useState<string | null>(null);
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);
  const [imageDataUrls, setImageDataUrls] = useState<Map<string, string>>(new Map());

  if (!attachments || attachments.trim() === "") {
    return null;
  }

  let parsedAttachments: Attachment[] = [];
  try {
    parsedAttachments = JSON.parse(attachments);
    console.log("MessageAttachments: Parsed attachments:", parsedAttachments);
  } catch (e) {
    console.error("Failed to parse attachments:", e, "Raw attachments:", attachments);
    return null;
  }

  if (parsedAttachments.length === 0) {
    console.log("MessageAttachments: No attachments found after parsing");
    return null;
  }

  // Load image data URLs
  useEffect(() => {
    const loadImages = async () => {
      const newDataUrls = new Map<string, string>();
      for (const attachment of parsedAttachments) {
        if (attachment.type === "image" || attachment.type === "video") {
          const url = attachment.thumbnail || attachment.url;
          if (url && !imageDataUrls.has(url)) {
            try {
              const dataUrl = await GetAttachmentData(url);
              newDataUrls.set(url, dataUrl);
            } catch (error) {
              console.error("Failed to load attachment:", error);
            }
          }
        }
      }
      if (newDataUrls.size > 0) {
        setImageDataUrls((prev) => {
          const updated = new Map(prev);
          newDataUrls.forEach((value, key) => updated.set(key, value));
          return updated;
        });
      }
    };
    if (parsedAttachments.length > 0) {
      loadImages();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [attachments]);

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
      const cachedDataUrl = imageDataUrls.get(url);
      if (cachedDataUrl) {
        setSelectedImage(cachedDataUrl);
        return;
      }
      
      try {
        const dataUrl = await GetAttachmentData(url);
        if (dataUrl) {
          setImageDataUrls((prev) => new Map(prev).set(url, dataUrl));
          setSelectedImage(dataUrl);
        }
      } catch (error) {
        console.error("Failed to load image:", error);
      }
    }
  };

  return (
    <>
      <div className="mt-2 space-y-2">
        {parsedAttachments.map((attachment, index) => {
          const Icon = getFileIcon(attachment.mimeType, attachment.type);
          const isImage = attachment.type === "image";
          const thumbnail = attachment.thumbnail || attachment.url;

          return (
            <div
              key={index}
              className={`relative group ${
                isFromMe ? "flex justify-end" : "flex justify-start"
              }`}
              onMouseEnter={() => setHoveredIndex(index)}
              onMouseLeave={() => setHoveredIndex(null)}
            >
              {isImage ? (
                <div
                  className="relative cursor-pointer rounded-lg overflow-hidden max-w-xs"
                  onClick={() => handleImageClick(attachment)}
                >
                  {thumbnail && imageDataUrls.get(thumbnail) ? (
                    <img
                      src={imageDataUrls.get(thumbnail)}
                      alt={attachment.fileName}
                      className="max-w-full h-auto rounded-lg"
                      style={{ maxHeight: "300px" }}
                    />
                  ) : (
                    <div className="w-48 h-48 bg-muted flex items-center justify-center rounded-lg">
                      <ImageIcon className="h-12 w-12 text-muted-foreground" />
                    </div>
                  )}
                  {hoveredIndex === index && (
                    <div className="absolute inset-0 bg-black/50 flex items-center justify-center rounded-lg">
                      <Download className="h-8 w-8 text-white" />
                    </div>
                  )}
                </div>
              ) : (
                <div
                  className={`flex items-center gap-3 p-3 rounded-lg border ${
                    isFromMe
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

      {/* Image preview dialog */}
      <Dialog open={selectedImage !== null} onOpenChange={() => setSelectedImage(null)}>
        <DialogContent className="max-w-4xl max-h-[90vh] p-0">
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
    </>
  );
}

