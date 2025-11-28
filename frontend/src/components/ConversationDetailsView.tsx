import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { GetContactAliases, GetMessagesForConversation, SendFile, SetContactAlias } from "../../wailsjs/go/main/App";
import { useCallback, useMemo, useState } from "react";
import { useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { FileUploadModal } from "./FileUploadModal";
import { Input } from "@/components/ui/input";
import { X } from "lucide-react";
import { cn, timeToDate } from "@/lib/utils";
import type { models } from "../../wailsjs/go/models";
import { translateBackendMessage } from "@/lib/i18n-helpers";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";

// Declare SendFileFromPath as it will be available after Wails bindings are regenerated
declare const SendFileFromPath: ((conversationID: string, filePath: string) => Promise<models.Message>) | undefined;

async function compressImageFile(file: File): Promise<File> {
  const isImage = file.type?.startsWith("image/");
  const shouldCompress = isImage && file.size > 1024 * 1024;
  if (!shouldCompress) {
    return file;
  }

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
    if (!ctx) {
      imageBitmap.close();
      return file;
    }
    ctx.drawImage(imageBitmap, 0, 0, width, height);
    imageBitmap.close();

    const blob = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob((result) => resolve(result), "image/jpeg", 0.85)
    );

    if (!blob) {
      return file;
    }

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

// Get display name for a message sender
function getSenderDisplayName(
  senderName: string | undefined,
  senderId: string,
  isFromMe: boolean,
  t: (key: string) => string
): string {
  if (isFromMe) return t("you") || "You";
  if (senderName && senderName.trim().length > 0) {
    return senderName;
  }
  
  // For WhatsApp IDs like "33631207926@s.whatsapp.net", extract and format the phone number
  const whatsappMatch = senderId.match(/^(\d+)@s\.whatsapp\.net$/);
  if (whatsappMatch) {
    const phoneNumber = whatsappMatch[1];
    // Format phone number with spaces for readability
    if (phoneNumber.startsWith("33") && phoneNumber.length >= 10) {
      // French phone number format: +33 followed by 9 digits (without leading 0)
      const countryCode = phoneNumber.substring(0, 2);
      const rest = phoneNumber.substring(2);
      const formatted = `+${countryCode} ${rest.substring(0, 1)} ${rest.substring(1, 3)} ${rest.substring(3, 5)} ${rest.substring(5, 7)} ${rest.substring(7)}`;
      return formatted;
    } else {
      // Other formats: add spaces every 2 digits
      const formatted = phoneNumber.replace(/(\d{2})(?=\d)/g, "$1 ");
      return `+${formatted}`;
    }
  }
  
  // Fallback for other ID formats
  return senderId
    .replace(/^user-/, "")
    .replace(/^whatsapp-/, "")
    .replace(/^slack-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

const fetchMessages = async (conversationID: string) => {
  return GetMessagesForConversation(conversationID);
};

interface ConversationDetailsViewProps {
  selectedConversation: models.MetaContact;
}

export function ConversationDetailsView({
  selectedConversation,
}: ConversationDetailsViewProps) {
  const { t } = useTranslation();
  const setShowConversationDetails = useAppStore(
    (state) => state.setShowConversationDetails
  );
  const setSelectedAvatarUrl = useAppStore(
    (state) => state.setSelectedAvatarUrl
  );
  const [isFileUploadModalOpen, setIsFileUploadModalOpen] = useState(false);
  const [pendingFiles, setPendingFiles] = useState<File[]>([]);
  const [isDragging, setIsDragging] = useState(false);

  const queryClient = useQueryClient();
  const conversationId = selectedConversation.linkedAccounts[0]?.userId ?? "";
  const { data: messages } = useSuspenseQuery<models.Message[], Error>({
    queryKey: ["messages", conversationId],
    queryFn: () => fetchMessages(conversationId),
  });

  const { data: aliases = {} } = useQuery<Record<string, string>, Error>({
    queryKey: ["contactAliases"],
    queryFn: async () => {
      const aliasMap = await GetContactAliases();
      return aliasMap || {};
    },
  });

  // Extract unique participants from messages
  const participants = useMemo(() => {
    const participantMap = new Map<
      string,
      {
        senderId: string;
        senderName: string | undefined;
        senderAvatarUrl: string | undefined;
        isFromMe: boolean;
        lastMessageTime: Date;
      }
    >();

    messages.forEach((msg) => {
      const existing = participantMap.get(msg.senderId);
      const msgTime = timeToDate(msg.timestamp);
      
      if (!existing || msgTime > existing.lastMessageTime) {
        participantMap.set(msg.senderId, {
          senderId: msg.senderId,
          senderName: msg.senderName,
          senderAvatarUrl: msg.senderAvatarUrl,
          isFromMe: msg.isFromMe,
          lastMessageTime: msgTime,
        });
      }
    });

    return Array.from(participantMap.values()).sort((a, b) => {
      // Sort by last message time, most recent first
      return b.lastMessageTime.getTime() - a.lastMessageTime.getTime();
    });
  }, [messages]);

  const handleClose = () => {
    setShowConversationDetails(false);
  };

  const handleAvatarClick = (avatarUrl: string | undefined, displayName: string) => {
    // Use avatar URL if available, otherwise use a placeholder
    const urlToShow = avatarUrl || `https://api.dicebear.com/7.x/initials/svg?seed=${encodeURIComponent(displayName)}`;
    setSelectedAvatarUrl(urlToShow);
  };

  const getDisplayNameWithAlias = (
    senderName: string | undefined,
    senderId: string,
    isFromMe: boolean
  ): string => {
    // Check if there's a custom alias
    if (aliases[senderId]) {
      return aliases[senderId];
    }
    return getSenderDisplayName(senderName, senderId, isFromMe, t);
  };

  // Handle drag and drop for file upload
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
    if (files.length > 0) {
      setPendingFiles(files);
      setIsFileUploadModalOpen(true);
    }
  }, []);

  const handleFileUpload = async (files: File[], filePaths?: string[]) => {
    if (!selectedConversation || (files.length === 0 && (!filePaths || filePaths.length === 0))) {
      return;
    }

    const convId = selectedConversation.linkedAccounts[0]?.userId;
    if (!convId) {
      return;
    }

    // First, handle file paths (from clipboard/drag&drop in Wails)
    if (filePaths && filePaths.length > 0) {
      for (const filePath of filePaths) {
        try {
          if (typeof SendFileFromPath === "function") {
            await SendFileFromPath(convId, filePath);
          } else {
            console.error("SendFileFromPath API is not available. Please rebuild the application.");
          }
        } catch (error) {
          console.error("Failed to send file from path:", filePath, error);
        }
      }
    }

    // Then handle File objects (from file picker)
    if (files.length === 0) {
      // Only file paths, no File objects
      queryClient.invalidateQueries({
        queryKey: ["messages", convId],
      });
      queryClient.refetchQueries({
        queryKey: ["messages", convId],
      });
      return;
    }

    // Check if SendFile is available
    if (typeof SendFile !== "function") {
      console.error("SendFile API is not available. Please rebuild the application to generate Wails bindings.");
      return;
    }

    // Send each file
    for (const initialFile of files) {
      let file = initialFile;
      try {

        // Check file size (limit to 64MB to avoid memory issues)
        const maxSize = 64 * 1024 * 1024; // 64MB
        if (file.size > maxSize) {
          console.error(`File ${file.name} is too large (${(file.size / 1024 / 1024).toFixed(2)}MB). Maximum size is 64MB.`);
          continue;
        }

        // In Wails, files from clipboard/drag&drop may have a path property
        // Check all possible ways to get the file path
        interface FileWithPath {
          path?: string;
          webkitRelativePath?: string;
          [key: string]: unknown;
        }
        const fileWithPath = file as File & FileWithPath;
        
        // Try multiple ways to get the path
        let filePath: string | undefined = undefined;
        
        // Method 1: Direct path property (Wails-specific)
        if (fileWithPath.path && typeof fileWithPath.path === "string") {
          filePath = fileWithPath.path;
        }
        // Method 2: webkitRelativePath (may contain full path in some cases)
        else if (fileWithPath.webkitRelativePath && typeof fileWithPath.webkitRelativePath === "string") {
          // webkitRelativePath is usually relative, but check if it looks like an absolute path
          if (fileWithPath.webkitRelativePath.startsWith("/") || fileWithPath.webkitRelativePath.match(/^[A-Za-z]:/)) {
            filePath = fileWithPath.webkitRelativePath;
          }
        }
        // Method 3: Check all properties for any string that looks like a file path
        else {
          for (const key in fileWithPath) {
            if (Object.prototype.hasOwnProperty.call(fileWithPath, key)) {
              const value = fileWithPath[key];
              if (typeof value === "string" && (value.startsWith("/") || value.match(/^[A-Za-z]:[\\/]/))) {
                // This looks like a file path
                filePath = value;
                break;
              }
            }
          }
        }
        
        if (filePath && typeof filePath === "string") {
          // File has a path - use SendFileFromPath directly
          if (typeof SendFileFromPath === "function") {
            await SendFileFromPath(convId, filePath);
            continue;
          } else {
            console.warn("SendFileFromPath not available yet, falling back to reading file");
          }
        }
        
        // Attempt to compress image before reading
        file = await compressImageFile(file);

        // File doesn't have a path - use FileReader to convert to base64
        // This is the correct approach for Wails/WebKit (avoids WebKitBlobResource error 4)
        let fileData: string;
        try {
          fileData = await new Promise<string>((resolve, reject) => {
            const reader = new FileReader();
            
            // Set timeout to avoid hanging
            const timeout = setTimeout(() => {
              reader.abort();
              reject(new Error(`Timeout reading file: ${file.name}`));
            }, 30000); // 30 seconds timeout
            
            reader.onload = (e) => {
              clearTimeout(timeout);
              try {
                if (e.target?.result && typeof e.target.result === "string") {
                  // Remove data URL prefix (e.g., "data:image/jpeg;base64,")
                  const parts = e.target.result.split(",");
                  if (parts.length > 1) {
                    resolve(parts[1]); // Return only the base64 data part
                  } else {
                    reject(new Error("Invalid data URL format"));
                  }
                } else {
                  reject(new Error("FileReader result is empty or invalid"));
                }
              } catch (err) {
                clearTimeout(timeout);
                reject(err);
              }
            };
            
            reader.onerror = (error) => {
              clearTimeout(timeout);
              console.error("FileReader error for file:", file.name, error);
              reject(new Error(`Failed to read file: ${file.name} (size: ${(file.size / 1024 / 1024).toFixed(2)}MB)`));
            };
            
            reader.onabort = () => {
              clearTimeout(timeout);
              reject(new Error(`File reading aborted: ${file.name}`));
            };
            
            // Use readAsDataURL to convert file to base64
            try {
              reader.readAsDataURL(file);
            } catch (err) {
              clearTimeout(timeout);
              reject(new Error(`Failed to start reading file: ${file.name} - ${err}`));
            }
          });
        } catch (readerError) {
          console.error("Cannot read file with FileReader:", readerError);
          throw new Error(`Cannot read file "${file.name}". Please try using the file picker button (📎) instead.`);
        }

        // Send file via API using base64 data
        if (typeof SendFile === "function") {
        await SendFile(convId, fileData, file.name, file.type);
        } else {
          throw new Error("SendFile API is not available");
        }
      } catch (error) {
        console.error("Failed to send file:", file.name, error);
        // Continue with other files even if one fails
      }
    }

    // Invalidate and refetch messages after sending
    queryClient.invalidateQueries({
      queryKey: ["messages", convId],
    });
    queryClient.refetchQueries({
      queryKey: ["messages", convId],
    });
  };

  return (
    <div 
      className={cn(
        "flex flex-col h-full transition-colors",
        isDragging && "bg-muted/50"
      )}
      onDragEnter={handleDragEnter}
      onDragLeave={handleDragLeave}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      <div className="p-4 border-b flex justify-between items-center shrink-0">
        <h3 className="text-md font-semibold">{t("conversation_details")}</h3>
        <Button variant="ghost" size="icon" onClick={handleClose}>
          <X className="h-4 w-4" />
        </Button>
      </div>
      <div className="flex-1 overflow-y-auto p-4 min-h-0">
        <div className="space-y-6">
          {/* Participants */}
          <div>
            <h4 className="text-sm font-semibold text-muted-foreground mb-3">
              {t("participants")} ({participants.length})
            </h4>
            <div className="space-y-3">
              {participants.map((participant) => {
                const displayName = getDisplayNameWithAlias(
                  participant.senderName,
                  participant.senderId,
                  participant.isFromMe
                );
                const status = selectedConversation.linkedAccounts.find(
                  (acc) => acc.userId === participant.senderId
                )?.status || t("offline");

                return (
                  <ParticipantItem
                    key={participant.senderId}
                    participant={participant}
                    displayName={displayName}
                    status={status}
                    alias={aliases[participant.senderId]}
                    onAvatarClick={handleAvatarClick}
                    onAliasChange={async (newAlias: string) => {
                      await SetContactAlias(participant.senderId, newAlias);
                      queryClient.invalidateQueries({ queryKey: ["contactAliases"] });
                      queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
                    }}
                  />
                );
              })}
            </div>
          </div>
        </div>
      </div>
      <FileUploadModal
        open={isFileUploadModalOpen}
        onOpenChange={setIsFileUploadModalOpen}
        files={pendingFiles}
        onConfirm={handleFileUpload}
      />
    </div>
  );
}

interface ParticipantItemProps {
  participant: {
    senderId: string;
    senderName: string | undefined;
    senderAvatarUrl: string | undefined;
    isFromMe: boolean;
  };
  displayName: string;
  status: string;
  alias?: string;
  onAvatarClick: (avatarUrl: string | undefined, displayName: string) => void;
  onAliasChange: (newAlias: string) => Promise<void>;
}

function ParticipantItem({
  participant,
  displayName,
  status,
  alias,
  onAvatarClick,
  onAliasChange,
}: ParticipantItemProps) {
  const { t } = useTranslation();
  const [isEditing, setIsEditing] = useState(false);
  const [editValue, setEditValue] = useState(displayName);

  const handleSave = async () => {
    await onAliasChange(editValue.trim());
    setIsEditing(false);
  };

  const handleCancel = () => {
    setEditValue(displayName);
    setIsEditing(false);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") {
      handleSave();
    } else if (e.key === "Escape") {
      handleCancel();
    }
  };

  if (participant.isFromMe) {
    // Don't allow editing "You"
    return (
      <div className="flex items-center gap-3 p-2 rounded-lg hover:bg-muted/50 transition-colors">
        <button
          onClick={() => onAvatarClick(participant.senderAvatarUrl, displayName)}
          className="shrink-0"
        >
          <Avatar className="h-10 w-10 cursor-pointer hover:opacity-80 transition-opacity">
            <AvatarImage src={participant.senderAvatarUrl} />
            <AvatarFallback>
              {displayName.substring(0, 2).toUpperCase()}
            </AvatarFallback>
          </Avatar>
        </button>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <p className="font-medium text-sm truncate">{displayName}</p>
            <span className="text-xs text-muted-foreground">({t("you")})</span>
          </div>
          <div className="flex items-center gap-2 mt-1">
            <span
              className={`h-2 w-2 rounded-full ${
                status === "online"
                  ? "bg-green-500"
                  : status === "away"
                  ? "bg-yellow-500"
                  : status === "busy"
                  ? "bg-red-500"
                  : "bg-gray-500"
              }`}
            />
            <p className="text-xs text-muted-foreground capitalize">{translateBackendMessage(status)}</p>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div
      className="flex items-center gap-3 p-2 rounded-lg hover:bg-muted/50 transition-colors group"
      onMouseEnter={() => {
        if (!isEditing) {
          setEditValue(displayName);
        }
      }}
    >
      <button
        onClick={() => onAvatarClick(participant.senderAvatarUrl, displayName)}
        className="shrink-0"
      >
        <Avatar className="h-10 w-10 cursor-pointer hover:opacity-80 transition-opacity">
          <AvatarImage src={participant.senderAvatarUrl} />
          <AvatarFallback>
            {displayName.substring(0, 2).toUpperCase()}
          </AvatarFallback>
        </Avatar>
      </button>
      <div className="flex-1 min-w-0">
        {isEditing ? (
          <div className="flex items-center gap-2">
            <Input
              value={editValue}
              onChange={(e) => setEditValue(e.target.value)}
              onKeyDown={handleKeyDown}
              onBlur={handleSave}
              className="h-7 text-sm"
              autoFocus
            />
            <Button
              variant="ghost"
              size="sm"
              onClick={handleCancel}
              className="h-7 px-2"
            >
              <X className="h-3 w-3" />
            </Button>
          </div>
        ) : (
          <div
            className="flex items-center gap-2 cursor-pointer"
            onClick={() => setIsEditing(true)}
            title={t("click_to_edit_name")}
          >
            <p className="font-medium text-sm truncate">{displayName}</p>
            {alias && (
              <span className="text-xs text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity">
                ({t("custom")})
              </span>
            )}
          </div>
        )}
        <div className="flex items-center gap-2 mt-1">
          <span
            className={`h-2 w-2 rounded-full ${
              status === "online"
                ? "bg-green-500"
                : status === "away"
                ? "bg-yellow-500"
                : status === "busy"
                ? "bg-red-500"
                : "bg-gray-500"
            }`}
          />
          <p className="text-xs text-muted-foreground capitalize">{translateBackendMessage(status)}</p>
        </div>
      </div>
    </div>
  );
}

