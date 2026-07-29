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

export interface UploadState {
  isUploading: boolean;
  currentFileIndex: number;
  totalFiles: number;
  currentFileName: string;
  progressPercent: number;
  fileProgressPercent: number;
  statusText?: string;
  error?: string | null;
}

export function useFileUpload(conversationId: string, showToast?: (message: string, type?: "error" | "info" | "success") => void) {
  const queryClient = useQueryClient();
  const [isDragging, setIsDragging] = useState(false);
  const [isFileUploadModalOpen, setIsFileUploadModalOpen] = useState(false);
  const [pendingFiles, setPendingFiles] = useState<File[]>([]);
  const [pendingFilePaths, setPendingFilePaths] = useState<string[]>([]);
  const [uploadState, setUploadState] = useState<UploadState>({
    isUploading: false,
    currentFileIndex: 0,
    totalFiles: 0,
    currentFileName: "",
    progressPercent: 0,
    fileProgressPercent: 0,
    statusText: "",
    error: null,
  });

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

    const totalCount = (filePaths ? filePaths.length : 0) + files.length;
    setUploadState({
      isUploading: true,
      currentFileIndex: 1,
      totalFiles: totalCount,
      currentFileName: filePaths?.[0]?.split(/[/\\]/).pop() || files[0]?.name || "",
      progressPercent: 5,
      fileProgressPercent: 0,
      statusText: "",
      error: null,
    });

    let currentItemIndex = 0;

    if (hasFilePaths && filePaths) {
      for (let idx = 0; idx < filePaths.length; idx++) {
        const filePath = filePaths[idx];
        currentItemIndex = idx + 1;
        const fileName = filePath.split(/[/\\]/).pop() || filePath;
        const basePercent = Math.round(((currentItemIndex - 1) / totalCount) * 100);

        setUploadState({
          isUploading: true,
          currentFileIndex: currentItemIndex,
          totalFiles: totalCount,
          currentFileName: fileName,
          progressPercent: Math.max(5, basePercent),
          fileProgressPercent: 50,
          error: null,
        });

        try {
          if (typeof SendFileFromPath === "function") {
            await SendFileFromPath(conversationId, filePath);
          } else {
            console.error("SendFileFromPath API is not available. Please rebuild the application.");
          }
        } catch (error) {
          console.error("Failed to send file from path:", filePath, error);
        }

        const completedPercent = Math.round((currentItemIndex / totalCount) * 100);
        setUploadState({
          isUploading: true,
          currentFileIndex: currentItemIndex,
          totalFiles: totalCount,
          currentFileName: fileName,
          progressPercent: completedPercent,
          fileProgressPercent: 100,
          error: null,
        });
      }
    }

    const shouldProcessFileObjects = files.length > 0;
    if (!shouldProcessFileObjects) {
      setUploadState(prev => ({ ...prev, progressPercent: 100 }));
      setTimeout(() => {
        setUploadState({
          isUploading: false,
          currentFileIndex: 0,
          totalFiles: 0,
          currentFileName: "",
          progressPercent: 0,
          fileProgressPercent: 0,
          statusText: "",
          error: null,
        });
      }, 500);
      refreshMessages();
      return;
    }

    if (typeof SendFile !== "function") {
      console.error("SendFile API is not available. Please rebuild the application to generate Wails bindings.");
      setUploadState(prev => ({ ...prev, isUploading: false, error: "SendFile API not available" }));
      return;
    }

    interface FileWithPath {
      path?: string;
      webkitRelativePath?: string;
      [key: string]: unknown;
    }

    const pathOffset = filePaths ? filePaths.length : 0;

    for (let i = 0; i < files.length; i++) {
      let file = files[i];
      currentItemIndex = pathOffset + i + 1;
      const basePercent = Math.round(((currentItemIndex - 1) / totalCount) * 100);

      setUploadState({
        isUploading: true,
        currentFileIndex: currentItemIndex,
        totalFiles: totalCount,
        currentFileName: file.name,
        progressPercent: Math.max(5, basePercent),
        fileProgressPercent: 10,
        error: null,
      });

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
            const compPercent = Math.round((currentItemIndex / totalCount) * 100);
            setUploadState({
              isUploading: true,
              currentFileIndex: currentItemIndex,
              totalFiles: totalCount,
              currentFileName: file.name,
              progressPercent: compPercent,
              fileProgressPercent: 100,
              error: null,
            });
            continue;
          } else {
            console.warn("SendFileFromPath not available yet, falling back to reading file");
          }
        }

        if (file.type?.startsWith("image/") && file.size > 1024 * 1024) {
          setUploadState(prev => ({ ...prev, statusText: "compressing" }));
        }

        file = await compressImageFile(file);

        let fileData: string;
        let fileMimeType = file.type || "application/octet-stream";
        let fileName = file.name;

        setUploadState(prev => ({ ...prev, statusText: undefined, fileProgressPercent: 20 }));

        try {
          fileData = await new Promise<string>((resolve, reject) => {
            const reader = new FileReader();
            const timeout = setTimeout(() => { reader.abort(); reject(new Error(`Timeout reading file: ${file.name}`)); }, 30000);

            reader.onprogress = (e) => {
              if (e.lengthComputable && e.total > 0) {
                const fPct = Math.round((e.loaded / e.total) * 100);
                const oPct = Math.round(((currentItemIndex - 1 + (fPct / 100) * 0.7) / totalCount) * 100);
                setUploadState(prev => ({
                  ...prev,
                  fileProgressPercent: fPct,
                  progressPercent: Math.max(prev.progressPercent, oPct),
                }));
              }
            };

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

        setUploadState(prev => ({
          ...prev,
          fileProgressPercent: 90,
          progressPercent: Math.round(((currentItemIndex - 0.1) / totalCount) * 100),
        }));

        if (typeof SendFile === "function") {
          await SendFile(conversationId, fileData, fileName, fileMimeType);
        } else {
          throw new Error("SendFile API is not available");
        }

        const compPct = Math.round((currentItemIndex / totalCount) * 100);
        setUploadState({
          isUploading: true,
          currentFileIndex: currentItemIndex,
          totalFiles: totalCount,
          currentFileName: file.name,
          progressPercent: compPct,
          fileProgressPercent: 100,
          error: null,
        });
      } catch (error) {
        const errorMsg = error instanceof Error ? error.message : String(error);
        console.error("Failed to send file:", file.name, errorMsg);
        setUploadState(prev => ({ ...prev, isUploading: false, error: errorMsg }));
        if (showToast) showToast(errorMsg, "error");
        refreshMessages();
        return;
      }
    }

    setUploadState(prev => ({ ...prev, progressPercent: 100 }));
    setTimeout(() => {
      setUploadState({
        isUploading: false,
        currentFileIndex: 0,
        totalFiles: 0,
        currentFileName: "",
        progressPercent: 0,
        fileProgressPercent: 0,
        statusText: "",
        error: null,
      });
    }, 500);

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
    uploadState,
    handleDragEnter,
    handleDragLeave,
    handleDragOver,
    handleDrop,
    handleFileUpload,
    handleRetrySend,
    handleDeleteLocalMessage,
  };
}
