import { GetContactAliases, GetMessagesForConversation, SendFile } from "../../wailsjs/go/main/App";
import { cn } from "@/lib/utils";
import { useCallback, useMemo, useState } from "react";
import { useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { Suspense } from "react";

import { Button } from "@/components/ui/button";
import { FileUploadModal } from "./FileUploadModal";
import { X } from "lucide-react";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";
import { ParticipantsList } from "./ParticipantsList";
import { ParticipantListSkeleton } from "./ParticipantListSkeleton";

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
  const [participantsCount, setParticipantsCount] = useState<number | null>(null);

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

  const handleClose = () => {
    setShowConversationDetails(false);
  };

  const handleAvatarClick = (avatarUrl: string | undefined, displayName: string) => {
    // Use avatar URL if available, otherwise use a placeholder
    const urlToShow = avatarUrl || `https://api.dicebear.com/7.x/initials/svg?seed=${encodeURIComponent(displayName)}`;
    setSelectedAvatarUrl(urlToShow);
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
      <div className="flex-1 overflow-y-auto p-4 min-h-0 scroll-area">
        <div className="space-y-6">
          {/* Participants */}
          <div>
            <h4 className="text-sm font-semibold text-muted-foreground mb-3">
              {t("participants")}{participantsCount !== null ? ` (${participantsCount})` : ""}
            </h4>
            <Suspense fallback={<ParticipantListSkeleton />}>
              <ParticipantsList
                conversationId={conversationId}
                messages={messages}
                selectedConversation={selectedConversation}
                aliases={aliases}
                onAvatarClick={handleAvatarClick}
                onParticipantsCountChange={setParticipantsCount}
              />
            </Suspense>
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

