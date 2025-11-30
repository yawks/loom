import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { GetContactAliases, GetGroupParticipants, GetMessagesForConversation, GetParticipantNames, SendFile, SetContactAlias } from "../../wailsjs/go/main/App";
import { cn, timeToDate } from "@/lib/utils";
import { useCallback, useMemo, useState } from "react";
import { useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { FileUploadModal } from "./FileUploadModal";
import { Input } from "@/components/ui/input";
import { X } from "lucide-react";
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
  // Robust handling: extract local part from various WhatsApp ID formats
  // Supports: "33603018166@s.whatsapp.net", "186560595132538:6@lid", "187119343554767:7@lid"
  let phoneNumber: string | null = null;
  
  // Match "digits" optionally followed by ":digits@server"
  const match = senderId.match(/^(\d+)(?::\d+)?@/);
  if (match) {
    phoneNumber = match[1];
  }
  
  if (phoneNumber) {
    // If this looks like a French number (starts with 33 and 11 digits) format nicely
    if (phoneNumber.startsWith("33") && phoneNumber.length === 11) {
      const countryCode = phoneNumber.substring(0, 2); // "33"
      const rest = phoneNumber.substring(2); // 9 digits
      const formatted = `+${countryCode} ${rest.substring(0, 1)} ${rest.substring(1, 3)} ${rest.substring(3, 5)} ${rest.substring(5, 7)} ${rest.substring(7, 9)}`;
      return formatted;
    }
    // For other numeric local parts, return with a leading + and no odd grouping
    return `+${phoneNumber}`;
  }

  // Fallback for other ID formats: try to return a readable label
  return senderId
    .replace(/^user-/, "")
    .replace(/^whatsapp-/, "")
    .replace(/^slack-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

const fetchMessages = async (conversationID: string): Promise<models.Message[]> => {
  const result = await GetMessagesForConversation(conversationID);
  // Ensure we always return an array
  return Array.isArray(result) ? result : [];
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
  
  // Use a different query key to avoid conflicts with MessageList's useInfiniteQuery
  const { data: messagesData } = useSuspenseQuery<models.Message[], Error>({
    queryKey: ["messages-details", conversationId],
    queryFn: () => fetchMessages(conversationId),
  });
  
  // Ensure messages is always an array
  const messages = useMemo(() => {
    if (!messagesData || !Array.isArray(messagesData)) {
      return [];
    }
    return messagesData;
  }, [messagesData]);

  const { data: aliases = {} } = useQuery<Record<string, string>, Error>({
    queryKey: ["contactAliases"],
    queryFn: async () => {
      const aliasMap = await GetContactAliases();
      return aliasMap || {};
    },
  });

  // Query for group participants from the provider
  const { data: groupParticipantsData = [] } = useQuery<models.GroupParticipant[], Error>({
    queryKey: ["groupParticipants", conversationId],
    queryFn: () => GetGroupParticipants(conversationId),
    enabled: !!conversationId,
  });

  // Get participant names from the provider (contacts)
  const { data: participantNames = {} } = useQuery<Record<string, string>, Error>({
    queryKey: ["participantNames", conversationId],
    queryFn: async () => {
      if (!groupParticipantsData || groupParticipantsData.length === 0) {
        return {};
      }
      const ids = groupParticipantsData.map((p) => p.userId);
      try {
        return await GetParticipantNames(ids);
      } catch (err) {
        console.error("Failed to get participant names:", err);
        return {};
      }
    },
    enabled: !!conversationId && groupParticipantsData.length > 0,
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
        isAdmin?: boolean;
        joinedAt?: Date;
      }
    >();

    // Determine the current user's ID by finding messages marked as isFromMe
    let currentUserId: string | undefined;
    if (messages && Array.isArray(messages)) {
      for (const msg of messages) {
        if (msg.isFromMe && msg.senderId) {
          currentUserId = msg.senderId;
          break;
        }
      }
    }

    // First, add participants from the provider (group participants)
    if (groupParticipantsData && Array.isArray(groupParticipantsData)) {
      groupParticipantsData.forEach((participant) => {
        if (!participantMap.has(participant.userId)) {
          const joinedAtDate = participant.joinedAt ? timeToDate(participant.joinedAt) : new Date();
          // Use the provider's contact name first (from GetParticipantNames)
          const providerName = participantNames[participant.userId];
          participantMap.set(participant.userId, {
            senderId: participant.userId,
            senderName: providerName || undefined, // Will be populated from messages or aliases if not found
            senderAvatarUrl: undefined,
            isFromMe: currentUserId ? participant.userId === currentUserId : false,
            lastMessageTime: joinedAtDate,
            isAdmin: participant.isAdmin,
            joinedAt: joinedAtDate,
          });
        } else {
          // Update with provider info
          const existing = participantMap.get(participant.userId);
          if (existing) {
            existing.isAdmin = participant.isAdmin;
            existing.joinedAt = participant.joinedAt ? timeToDate(participant.joinedAt) : new Date();
            // Use provider name if not already set
            if (!existing.senderName) {
              existing.senderName = participantNames[participant.userId];
            }
            // Ensure isFromMe is correctly set based on currentUserId
            existing.isFromMe = currentUserId ? participant.userId === currentUserId : false;
          }
        }
      });
    }

    // Ensure messages is an array before iterating
    if (messages && Array.isArray(messages)) {
      messages.forEach((msg) => {
        // Skip messages from senders with malformed IDs (e.g., "186560595132538:6@lid")
        // These are internal WhatsApp metadata, not real participants
        if (/:\d+@/.test(msg.senderId)) {
          return; // Skip this sender
        }
        
        const existing = participantMap.get(msg.senderId);
        const msgTime = timeToDate(msg.timestamp);
        
        if (!existing) {
          participantMap.set(msg.senderId, {
            senderId: msg.senderId,
            senderName: msg.senderName,
            senderAvatarUrl: msg.senderAvatarUrl,
            isFromMe: msg.isFromMe && msg.senderId === currentUserId,
            lastMessageTime: msgTime,
          });
        } else {
          // Update with message info (name, avatar) BUT don't override provider names
          // Only use message name if we don't have a provider name already
          if (!existing.senderName && msg.senderName) {
            existing.senderName = msg.senderName;
          }
          if (msg.senderAvatarUrl) {
            existing.senderAvatarUrl = msg.senderAvatarUrl;
          }
          // Update last message time if newer
          if (msgTime > existing.lastMessageTime) {
            existing.lastMessageTime = msgTime;
          }
          // Update isFromMe based on currentUserId
          if (currentUserId) {
            existing.isFromMe = msg.senderId === currentUserId;
          }
        }
      });
    }

    return Array.from(participantMap.values()).sort((a, b) => {
      // Sort by last message time, most recent first
      return b.lastMessageTime.getTime() - a.lastMessageTime.getTime();
    });
  }, [messages, groupParticipantsData, participantNames]);

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
    // Check if there's a custom alias first
    if (aliases[senderId]) {
      return aliases[senderId];
    }
    
    // Use senderName if available, otherwise format the ID
    if (senderName && senderName.trim().length > 0) {
      return senderName;
    }
    
    // Fall back to formatting the ID itself
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
        queryKey: ["messages-details", convId],
      });
      queryClient.refetchQueries({
        queryKey: ["messages-details", convId],
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
      queryKey: ["messages-details", convId],
    });
    queryClient.refetchQueries({
      queryKey: ["messages-details", convId],
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
    isAdmin?: boolean;
    joinedAt?: Date;
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
            {participant.isAdmin && (
              <span className="text-xs bg-blue-600/20 text-blue-700 dark:text-blue-300 px-2 py-0.5 rounded">
                {t("admin")}
              </span>
            )}
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
            {participant.isAdmin && (
              <span className="text-xs bg-blue-600/20 text-blue-700 dark:text-blue-300 px-2 py-0.5 rounded">
                {t("admin")}
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

