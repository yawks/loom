import EmojiPicker, { Theme } from "emoji-picker-react";
import { Paperclip, Send, Smile, X } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Suspense, useCallback, useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient, type InfiniteData } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { SendMessage, SendReply } from "../../wailsjs/go/main/App";
import { cn } from "@/lib/utils";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";
import { models } from "../../wailsjs/go/models";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { getSenderDisplayName as getSenderDisplayNameUtil } from "@/lib/userDisplayNames";

interface ChatInputProps {
  onFileUploadRequest?: (files: File[], filePaths?: string[]) => void;
  replyingToMessage?: models.Message | null;
  onCancelReply?: () => void;
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

export function ChatInput({ onFileUploadRequest, replyingToMessage, onCancelReply }: ChatInputProps) {
  const { t } = useTranslation();
  const [message, setMessage] = useState("");
  const [isEmojiPickerOpen, setIsEmojiPickerOpen] = useState(false);
  const [isDragging, setIsDragging] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const selectedContact = useAppStore((state) => state.selectedContact);
  const theme = useAppStore((state) => state.theme);
  const queryClient = useQueryClient();

  // Auto-focus the textarea whenever the selected conversation changes
  useEffect(() => {
    if (textareaRef.current) {
      textareaRef.current.focus();
    }
  }, [selectedContact?.id]);

  const sendMessageMutation = useMutation({
    mutationFn: async ({ conversationId, text, quotedMessageId }: { conversationId: string; text: string; quotedMessageId?: string }) => {
      if (quotedMessageId) {
        return await SendReply(conversationId, text, quotedMessageId);
      }
      return await SendMessage(conversationId, text);
    },
    // Optimistic update so the message appears instantly
    onMutate: async ({ conversationId, text, quotedMessageId }) => {
      const tempId = `temp-${Date.now()}`;
      await queryClient.cancelQueries({ queryKey: ["messages", conversationId] });

      const previousData = queryClient.getQueryData<InfiniteData<models.Message[]>>(
        ["messages", conversationId]
      );

      const existingMessages =
        (previousData?.pages?.flat?.() as models.Message[] | undefined) ?? [];
      const currentUserId =
        existingMessages.find((m: models.Message) => m.isFromMe && m.senderId)?.senderId || "";

      const now = new Date();
      const optimisticMessage = models.Message.createFrom({
        protocolMsgId: tempId,
        protocolConvId: conversationId,
        body: text,
        senderId: currentUserId,
        timestamp: now.toISOString(),
        isFromMe: true,
        quotedMessageId,
        // Frontend-only status fields
        localStatus: "sending",
        tempId,
      } as any);

      queryClient.setQueryData<InfiniteData<models.Message[]>>(
        ["messages", conversationId],
        (old) => {
          if (!old || !Array.isArray(old.pages)) {
            return { pages: [[optimisticMessage]], pageParams: [] } as InfiniteData<models.Message[]>;
          }
          const newPages = [...old.pages];
          if (newPages.length === 0) {
            newPages.push([optimisticMessage]);
          } else if (Array.isArray(newPages[0])) {
            newPages[0] = [optimisticMessage, ...newPages[0]];
          } else {
            newPages[0] = [optimisticMessage];
          }
          return { ...old, pages: newPages };
        }
      );

      return { previousData, tempId, conversationId };
    },
    onError: (_error, _vars, context) => {
      if (!context) return;
      const { conversationId, tempId } = context;
      // Mark the optimistic message as failed
      queryClient.setQueryData<InfiniteData<models.Message[]>>(
        ["messages", conversationId],
        (old) => {
          if (!old || !old.pages) return old;
          const updatedPages = old.pages.map((page) =>
            Array.isArray(page)
              ? page.map((msg) =>
                  (msg as any).tempId === tempId
                    ? ({ ...msg, localStatus: "error" } as any)
                    : msg
                )
              : page
          );
          return { ...old, pages: updatedPages };
        }
      );
    },
    onSuccess: (result, _vars, context) => {
      if (!context) return;
      const { conversationId, tempId } = context;
      // Replace the optimistic message with the real one
      queryClient.setQueryData<InfiniteData<models.Message[]>>(
        ["messages", conversationId],
        (old) => {
          if (!old || !old.pages) return old;
          const updatedPages = old.pages.map((page) => {
            if (!Array.isArray(page)) return page;
            return page
              .map((msg) =>
                (msg as any).tempId === tempId || msg.protocolMsgId === tempId ? result : msg
              )
              // Ensure newest first
              .sort(
                (a, b) =>
                  new Date(b.timestamp as any).getTime() - new Date(a.timestamp as any).getTime()
              );
          });
          return { ...old, pages: updatedPages };
        }
      );
    },
    onSettled: (_data, _error, _vars, context) => {
      if (context?.conversationId) {
        queryClient.invalidateQueries({ queryKey: ["messages", context.conversationId] });
        // Invalidate last message to update sidebar preview
        queryClient.invalidateQueries({ queryKey: ["lastMessage", context.conversationId] });
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
      const quotedMessageId = replyingToMessage?.protocolMsgId;
      setMessage("");
      if (textareaRef.current) {
        textareaRef.current.style.height = "auto";
      }
      // Clear reply state after sending
      if (onCancelReply) {
        onCancelReply();
      }
      try {
        await sendMessageMutation.mutateAsync({
          conversationId: selectedContact.linkedAccounts[0].userId,
          text,
          quotedMessageId,
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

  // Get sender display name for reply preview
  const getSenderDisplayName = (message: models.Message): string => {
    return getSenderDisplayNameUtil(message.senderName, message.senderId, message.isFromMe, t);
  };

  return (
    <div className="flex flex-col">
      {/* Reply preview */}
      {replyingToMessage && (
        <div className="px-4 pt-3 pb-2 border-t bg-muted/30 flex items-center gap-3">
          <div className="flex-1 flex items-center gap-2 min-w-0">
            <Avatar className="h-6 w-6 shrink-0">
              <AvatarImage src={replyingToMessage.senderAvatarUrl} />
              <AvatarFallback className="text-xs">
                {getSenderDisplayName(replyingToMessage).substring(0, 2).toUpperCase()}
              </AvatarFallback>
            </Avatar>
            <div className="flex-1 min-w-0 text-left">
              <div className="text-xs font-medium text-muted-foreground text-left">
                {t("replying_to")} {getSenderDisplayName(replyingToMessage)}
              </div>
              <div className="text-sm text-foreground truncate text-left">
                {(() => {
                  const body = replyingToMessage.body;
                  if (body && body.trim().length > 0) {
                    return body.length > 50 ? `${body.substring(0, 50)}...` : body;
                  }

                  // Check for voice message in attachments
                  if (replyingToMessage.attachments) {
                    try {
                      const atts = JSON.parse(replyingToMessage.attachments);
                      if (Array.isArray(atts) && atts.length > 0 && atts[0].type === "voice") {
                        return "🎤 " + t("voice_message");
                      }
                    } catch (e) {
                      // ignore parse error
                    }
                  }

                  return t("empty_message");
                })()}
              </div>
            </div>
          </div>
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6 shrink-0"
            onClick={onCancelReply}
            title={t("cancel_reply")}
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
      )}

      {/* Message input */}
      <div
        className={cn(
          "p-4 border-t flex items-end space-x-2 transition-colors",
          isDragging && "bg-muted/50",
          replyingToMessage && "border-t-0"
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
            autoCorrect="off"
            autoCapitalize="none"
            spellCheck={false}
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
    </div>
  );
}
