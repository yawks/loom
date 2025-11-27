import EmojiPicker, { Theme } from "emoji-picker-react";
import { Paperclip, Send, Smile } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Suspense, useCallback, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { SendMessage } from "../../wailsjs/go/main/App";
import { cn } from "@/lib/utils";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";

interface ChatInputProps {
  onFileUploadRequest?: (files: File[], filePaths?: string[]) => void;
}

const normalizeClipboardPath = (rawValue: string | null): string | null => {
  if (!rawValue) {
    return null;
  }

  const trimmed = rawValue.trim();
  if (!trimmed) {
    return null;
  }

  // Handle file:// URLs (e.g., from Maccy, Raycast, etc.)
  if (trimmed.toLowerCase().startsWith("file://")) {
    try {
      const url = new URL(trimmed);
      let pathname = decodeURIComponent(url.pathname);

      // On Windows, pathname can be like /C:/Users/... -> remove leading slash
      if (/^\/[A-Za-z]:/.test(pathname)) {
        pathname = pathname.substring(1);
      }

      return pathname;
    } catch (err) {
      console.warn("Failed to parse file:// URL from clipboard:", trimmed, err);
      return trimmed.replace(/^file:\/\//i, "");
    }
  }

  return trimmed;
};

const extractPathsFromText = (text: string | null): string[] => {
  if (!text) {
    return [];
  }

  return text
    .split(/\r?\n/)
    .map((entry) => normalizeClipboardPath(entry))
    .filter(
      (normalizedPath): normalizedPath is string =>
        Boolean(
          normalizedPath &&
            (normalizedPath.startsWith("/") ||
              normalizedPath.match(/^[A-Za-z]:[\\/]/)) &&
            normalizedPath.match(/\.[a-zA-Z0-9]+$/)
        )
    );
};

export function ChatInput({ onFileUploadRequest }: ChatInputProps) {
  const { t } = useTranslation();
  const [message, setMessage] = useState("");
  const [isEmojiPickerOpen, setIsEmojiPickerOpen] = useState(false);
  const [isDragging, setIsDragging] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const selectedContact = useAppStore((state) => state.selectedContact);
  const theme = useAppStore((state) => state.theme);
  const queryClient = useQueryClient();

  const sendMessageMutation = useMutation({
    mutationFn: async ({ conversationId, text }: { conversationId: string; text: string }) => {
      return await SendMessage(conversationId, text);
    },
    onSuccess: () => {
      // Invalidate and refetch messages after sending
      if (selectedContact && selectedContact.linkedAccounts[0]?.userId) {
        const conversationId = selectedContact.linkedAccounts[0].userId;
        queryClient.invalidateQueries({
          queryKey: ["messages", conversationId],
        });
        // Force a refetch to ensure the new message appears
        queryClient.refetchQueries({
          queryKey: ["messages", conversationId],
        });
      }
    },
    onError: () => {
      // If sending fails, also invalidate to ensure we have the latest state
      if (selectedContact && selectedContact.linkedAccounts[0]?.userId) {
        const conversationId = selectedContact.linkedAccounts[0].userId;
        queryClient.invalidateQueries({
          queryKey: ["messages", conversationId],
        });
      }
    },
  });

  // Auto-resize textarea based on content
  const adjustTextareaHeight = useCallback(() => {
    const textarea = textareaRef.current;
    if (textarea) {
      textarea.style.height = "auto";
      const maxHeight = 200; // Maximum height in pixels
      const newHeight = Math.min(textarea.scrollHeight, maxHeight);
      textarea.style.height = `${newHeight}px`;
    }
  }, []);

  const handleMessageChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setMessage(e.target.value);
    adjustTextareaHeight();
  };

  const handleSendMessage = async () => {
    if (message.trim() && selectedContact) {
      const text = message.trim();
      setMessage("");
      if (textareaRef.current) {
        textareaRef.current.style.height = "auto";
      }
      try {
        await sendMessageMutation.mutateAsync({
          conversationId: selectedContact.linkedAccounts[0].userId,
          text,
        });
      } catch (error) {
        // Error handling is done in onError
        console.error("Failed to send message:", error);
      }
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSendMessage();
    }
    // Shift+Enter allows new line
  };

  const handleEmojiClick = (emojiData: { emoji: string }) => {
    setMessage((prev) => prev + emojiData.emoji);
    setIsEmojiPickerOpen(false);
    // Adjust height after emoji is added
    setTimeout(adjustTextareaHeight, 0);
  };

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files.length > 0) {
      const files = Array.from(e.target.files);
      if (onFileUploadRequest) {
        onFileUploadRequest(files);
      }
      // Reset file input
      if (fileInputRef.current) {
        fileInputRef.current.value = "";
      }
    }
  };

  // Handle paste event for files
  const handlePaste = useCallback((e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    // Persist event for async operations (React synthetic events)
    if (typeof e.persist === "function") {
      e.persist();
    }

    const clipboardData = e.clipboardData;
    const files: File[] = [];
    const filePaths: string[] = [];
    const asyncPathPromises: Promise<void>[] = [];

    // First, check the text that would be pasted (synchronous access)
    // In Wails, the pasted text might contain the file path
    const pastedText = clipboardData.getData("text/plain");
    if (pastedText) {
      const paths = extractPathsFromText(pastedText);
      if (paths.length > 0) {
        filePaths.push(...paths);
      }
    }

    const uriList = clipboardData.getData("text/uri-list");
    if (uriList) {
      const paths = extractPathsFromText(uriList);
      if (paths.length > 0) {
        filePaths.push(...paths);
      }
    }

    // Check clipboardData.files (most direct access)
    if (clipboardData.files && clipboardData.files.length > 0) {
      const fileList = Array.from(clipboardData.files);
      files.push(...fileList);
    }

    // Also check clipboardData.items for file items and paths
    if (clipboardData.items && clipboardData.items.length > 0) {
      for (let i = 0; i < clipboardData.items.length; i++) {
        const item = clipboardData.items[i];
        
        // Check for file type
        if (item.kind === "file") {
          const file = item.getAsFile();
          if (file) {
            // Avoid duplicates
            if (!files.some(f => f.name === file.name && f.size === file.size && f.lastModified === file.lastModified)) {
              files.push(file);
            }
          }
        }
        // Check for text that might be a file path (async, but we already checked synchronously above)
        else if (item.kind === "string") {
          asyncPathPromises.push(
            new Promise<void>((resolve) => {
              item.getAsString((text) => {
                const paths = extractPathsFromText(text);
                paths.forEach((path) => {
                  if (!filePaths.includes(path)) {
                    filePaths.push(path);
                  }
                });

                resolve();
              });
            })
          );
        }
      }
    }

    const finalizeUpload = () => {
      if (files.length > 0 || filePaths.length > 0) {
        e.preventDefault();
        if (onFileUploadRequest) {
          onFileUploadRequest(files, filePaths.length > 0 ? filePaths : undefined);
        }
      }
    };

    if (asyncPathPromises.length > 0) {
      Promise.all(asyncPathPromises).then(finalizeUpload);
    } else {
      finalizeUpload();
    }
  }, [onFileUploadRequest]);

  // Handle drag and drop
  const handleDragEnter = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.dataTransfer.types.includes("Files")) {
      setIsDragging(true);
    }
  }, []);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    // Only set dragging to false if we're leaving the drop zone
    if (!e.currentTarget.contains(e.relatedTarget as Node)) {
      setIsDragging(false);
    }
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
    if (files.length > 0 && onFileUploadRequest) {
      onFileUploadRequest(files);
    }
  }, [onFileUploadRequest]);

  const hasMessage = message.trim().length > 0;

  return (
    <div
      className={cn(
        "p-4 border-t flex items-end space-x-2 transition-colors",
        isDragging && "bg-muted/50"
      )}
      onDragEnter={handleDragEnter}
      onDragLeave={handleDragLeave}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      <div className="flex items-center space-x-2 flex-1">
        <Button
          variant="ghost"
          size="icon"
          onClick={() => fileInputRef.current?.click()}
          className="shrink-0"
          title={t("attach_files")}
        >
          <Paperclip className="h-5 w-5" />
        </Button>
        <input
          ref={fileInputRef}
          type="file"
          multiple
          className="hidden"
          onChange={handleFileSelect}
        />

        <Popover open={isEmojiPickerOpen} onOpenChange={setIsEmojiPickerOpen}>
          <PopoverTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="shrink-0"
              title={t("add_emoji")}
            >
              <Smile className="h-5 w-5" />
            </Button>
          </PopoverTrigger>
          <PopoverContent className="w-auto p-0 border-0" align="start">
            <Suspense fallback={<div className="w-[352px] h-[435px]" />}>
              <EmojiPicker
                onEmojiClick={handleEmojiClick}
                theme={theme === "dark" ? Theme.DARK : Theme.LIGHT}
                width={352}
                height={435}
              />
            </Suspense>
          </PopoverContent>
        </Popover>

        <textarea
          ref={textareaRef}
          value={message}
          onChange={handleMessageChange}
          onKeyDown={handleKeyDown}
          onPaste={handlePaste}
          placeholder={t("type_a_message")}
          className="flex-1 min-h-[40px] max-h-[200px] resize-none rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
          rows={1}
        />
      </div>

      {hasMessage && (
        <Button
          onClick={handleSendMessage}
          size="icon"
          className="shrink-0"
          title={t("send")}
        >
          <Send className="h-5 w-5" />
        </Button>
      )}
    </div>
  );
}
