import { SendFile, SendMessage, SendReply } from "../../wailsjs/go/main/App";
import { useCallback, useState } from "react";

import type { InfiniteData } from "@tanstack/react-query";
import { models } from "../../wailsjs/go/models";
import { useQueryClient } from "@tanstack/react-query";

// Available after Wails bindings are regenerated
declare const SendFileFromPath: ((conversationID: string, filePath: string) => Promise<models.Message>) | undefined;

async function compressImageFile(file: File): Promise<File> {
  const isImage = file.type?.startsWith("image/");
  const shouldCompress = isImage && file.size > 1024 * 1024;
  if (!shouldCompress) return file;

  try {
    const imageBitmap = await createImageBitmap(file);
    let { width, height } = imageBitmap;
    const maxDimension = Math.max(width, height);
    const targetMax = 1600;
    const scale = maxDimension > targetMax ? targetMax / maxDimension : 1;
    width = Math.round(width * scale);
    height = Math.round(height * scale);

    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const ctx = canvas.getContext("2d");
    if (!ctx) { imageBitmap.close(); return file; }
    ctx.drawImage(imageBitmap, 0, 0, width, height);
    imageBitmap.close();

    const blob = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob((result) => resolve(result), "image/jpeg", 0.85)
    );
    if (!blob) return file;

    return new File(
      [blob],
      file.name.replace(/\.(png|webp)$/i, ".jpg"),
      { type: "image/jpeg", lastModified: Date.now() }
    );
  } catch (error) {
    console.warn("Image compression failed, sending original file.", error);
    return file;
  }
}

export function useFileUpload(conversationId: string) {
  const queryClient = useQueryClient();
  const [isDragging, setIsDragging] = useState(false);
  const [isFileUploadModalOpen, setIsFileUploadModalOpen] = useState(false);
  const [pendingFiles, setPendingFiles] = useState<File[]>([]);
  const [pendingFilePaths, setPendingFilePaths] = useState<string[]>([]);

  const handleDragEnter = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.dataTransfer.types.includes("Files")) setIsDragging(true);
  }, []);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (!e.currentTarget.contains(e.relatedTarget as Node)) setIsDragging(false);
  }, []);

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  }, []);

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(false);
    const files = Array.from(e.dataTransfer.files);
    if (files.length > 0) {
      setPendingFiles(files);
      setIsFileUploadModalOpen(true);
    }
  }, []);

  const refreshMessages = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
    queryClient.refetchQueries({ queryKey: ["messages", conversationId] });
  }, [queryClient, conversationId]);

  const handleFileUpload = useCallback(async (files: File[], filePaths?: string[]) => {
    const hasFilePaths = Boolean(filePaths && filePaths.length > 0);
    if (!conversationId || (files.length === 0 && !hasFilePaths)) return;

    if (hasFilePaths && filePaths) {
      for (const filePath of filePaths) {
        try {
          if (typeof SendFileFromPath === "function") {
            await SendFileFromPath(conversationId, filePath);
          } else {
            console.error("SendFileFromPath API is not available. Please rebuild the application.");
          }
        } catch (error) {
          console.error("Failed to send file from path:", filePath, error);
        }
      }
    }

    const shouldProcessFileObjects = files.length > 0 && !hasFilePaths;
    if (!shouldProcessFileObjects) {
      refreshMessages();
      return;
    }

    if (typeof SendFile !== "function") {
      console.error("SendFile API is not available. Please rebuild the application to generate Wails bindings.");
      return;
    }

    interface FileWithPath {
      path?: string;
      webkitRelativePath?: string;
      [key: string]: unknown;
    }

    for (const initialFile of files) {
      let file = initialFile;
      try {
        const maxSize = 64 * 1024 * 1024;
        if (file.size > maxSize) {
          console.error(`File ${file.name} is too large (${(file.size / 1024 / 1024).toFixed(2)}MB). Maximum size is 64MB.`);
          continue;
        }

        const fileWithPath = file as File & FileWithPath;
        let filePath: string | undefined;

        if (fileWithPath.path && typeof fileWithPath.path === "string") {
          filePath = fileWithPath.path;
        } else if (fileWithPath.webkitRelativePath && typeof fileWithPath.webkitRelativePath === "string") {
          if (fileWithPath.webkitRelativePath.startsWith("/") || fileWithPath.webkitRelativePath.match(/^[A-Za-z]:/)) {
            filePath = fileWithPath.webkitRelativePath;
          }
        } else {
          for (const key in fileWithPath) {
            if (Object.prototype.hasOwnProperty.call(fileWithPath, key)) {
              const value = fileWithPath[key];
              if (typeof value === "string" && (value.startsWith("/") || value.match(/^[A-Za-z]:[\\/]/))) {
                filePath = value;
                break;
              }
            }
          }
        }

        if (filePath && typeof filePath === "string") {
          if (typeof SendFileFromPath === "function") {
            await SendFileFromPath(conversationId, filePath);
            continue;
          } else {
            console.warn("SendFileFromPath not available yet, falling back to reading file");
          }
        }

        file = await compressImageFile(file);

        let fileData: string;
        let fileMimeType = file.type || "application/octet-stream";
        let fileName = file.name;

        try {
          fileData = await new Promise<string>((resolve, reject) => {
            const reader = new FileReader();
            const timeout = setTimeout(() => { reader.abort(); reject(new Error(`Timeout reading file: ${file.name}`)); }, 30000);

            reader.onload = (e) => {
              clearTimeout(timeout);
              try {
                if (e.target?.result && typeof e.target.result === "string") {
                  const parts = e.target.result.split(",");
                  if (parts.length > 1) { resolve(parts[1]); }
                  else { reject(new Error("Invalid data URL format")); }
                } else {
                  reject(new Error("FileReader result is empty or invalid"));
                }
              } catch (err) { clearTimeout(timeout); reject(err); }
            };

            reader.onerror = (error) => {
              clearTimeout(timeout);
              console.error("FileReader error for file:", file.name, error);
              reject(new Error(`Failed to read file: ${file.name} (size: ${(file.size / 1024 / 1024).toFixed(2)}MB)`));
            };

            reader.onabort = () => { clearTimeout(timeout); reject(new Error(`File reading aborted: ${file.name}`)); };

            try { reader.readAsDataURL(file); }
            catch (err) { clearTimeout(timeout); reject(new Error(`Failed to start reading file: ${file.name} - ${err}`)); }
          });
        } catch (readerError) {
          console.warn("JS FileReader failed, trying Go clipboard fallback...", readerError);

          let getClipboardFileFn: (() => Promise<{ filename: string; base64: string; mimeType: string }>) | undefined;
          if (typeof window !== "undefined") {
            try {
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              getClipboardFileFn = (window as any).go?.main?.App?.GetClipboardFile;
            } catch { /* ignore */ }
          }

          if (getClipboardFileFn && typeof getClipboardFileFn === "function") {
            const clipboardFile = await getClipboardFileFn();
            if (clipboardFile?.base64) {
              fileData = clipboardFile.base64;
              fileMimeType = clipboardFile.mimeType || file.type || "application/octet-stream";
              fileName = clipboardFile.filename || file.name;
            } else {
              throw new Error("Go clipboard API returned empty result");
            }
          } else {
            throw new Error(`Cannot read file "${file.name}". Please use the file picker button instead.`);
          }
        }

        if (typeof SendFile === "function") {
          await SendFile(conversationId, fileData, fileName, fileMimeType);
        } else {
          throw new Error("SendFile API is not available");
        }
      } catch (error) {
        console.error("Failed to send file:", file.name, error);
      }
    }

    refreshMessages();
  }, [conversationId, refreshMessages]);

  // Optimistic message state helpers (used for retry/delete local)
  const markMessageState = useCallback(
    (convId: string, protocolMsgId: string, updates: Partial<models.Message>) => {
      queryClient.setQueryData<InfiniteData<models.Message[]>>(
        ["messages", convId],
        (oldData) => {
          if (!oldData || !Array.isArray(oldData.pages)) return oldData;
          const pages = oldData.pages.map((page) => {
            if (!Array.isArray(page)) return page as models.Message[];
            return (page as models.Message[]).map((msg) =>
              msg.protocolMsgId === protocolMsgId ? ({ ...(msg as object), ...updates } as models.Message) : msg
            );
          });
          return { ...oldData, pages: pages as models.Message[][] };
        }
      );
    },
    [queryClient]
  );

  const removeMessageFromCache = useCallback(
    (convId: string, protocolMsgId: string) => {
      queryClient.setQueryData<InfiniteData<models.Message[]>>(
        ["messages", convId],
        (oldData) => {
          if (!oldData || !Array.isArray(oldData.pages)) return oldData;
          const pages = oldData.pages.map((page) => {
            if (!Array.isArray(page)) return page as models.Message[];
            return (page as models.Message[]).filter((msg) => msg.protocolMsgId !== protocolMsgId);
          });
          return { ...oldData, pages: pages as models.Message[][] };
        }
      );
    },
    [queryClient]
  );

  const handleRetrySend = useCallback(
    async (message: models.Message) => {
      const convId = message.protocolConvId;
      const text = message.body || "";
      const quotedId = message.quotedMessageId || undefined;
      if (!convId || !text.trim()) return;

      markMessageState(convId, message.protocolMsgId, { isPending: true, sendFailed: false } as Partial<models.Message>);

      try {
        const sent = quotedId
          ? await SendReply(convId, text, quotedId)
          : await SendMessage(convId, text);

        queryClient.setQueryData<InfiniteData<models.Message[]>>(
          ["messages", convId],
          (oldData) => {
            if (!oldData || !Array.isArray(oldData.pages)) return oldData;
            const pages = oldData.pages.map((page) => {
              if (!Array.isArray(page)) return page as models.Message[];
              return (page as models.Message[]).map((msg) =>
                msg.protocolMsgId === message.protocolMsgId
                  ? ({ ...(sent as object), isPending: false, sendFailed: false } as unknown as models.Message)
                  : msg
              );
            });
            return { ...oldData, pages: pages as models.Message[][] };
          }
        );
      } catch (err) {
        console.error("Retry send failed", err);
        markMessageState(convId, message.protocolMsgId, { isPending: false, sendFailed: true } as Partial<models.Message>);
      }
    },
    [markMessageState, queryClient]
  );

  const handleDeleteLocalMessage = useCallback(
    (message: models.Message) => {
      const convId = message.protocolConvId;
      const msgId = message.protocolMsgId;
      if (!convId || !msgId) return;
      removeMessageFromCache(convId, msgId);
    },
    [removeMessageFromCache]
  );

  return {
    isDragging,
    isFileUploadModalOpen,
    setIsFileUploadModalOpen,
    pendingFiles,
    setPendingFiles,
    pendingFilePaths,
    setPendingFilePaths,
    handleDragEnter,
    handleDragLeave,
    handleDragOver,
    handleDrop,
    handleFileUpload,
    handleRetrySend,
    handleDeleteLocalMessage,
  };
}
