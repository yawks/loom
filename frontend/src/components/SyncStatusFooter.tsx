import { useEffect, useState, useRef } from "react";
import { useTranslation } from "react-i18next";

import { EventsOn } from "../../wailsjs/runtime/runtime";
import { Loader2 } from "lucide-react";
import { translateBackendMessage } from "@/lib/i18n-helpers";

interface SyncStatus {
  status: "fetching_contacts" | "fetching_history" | "fetching_avatars" | "completed" | "error" | null;
  message: string;
  conversationId?: string;
  progress: number;
}

export function SyncStatusFooter() {
  const { t } = useTranslation();
  const [syncStatus, setSyncStatus] = useState<SyncStatus | null>(null);
  const hasCompletedRef = useRef(false);

  useEffect(() => {
    let timeoutId: ReturnType<typeof setTimeout> | null = null;

    const unsubscribe = EventsOn("sync-status", (statusJSON: string) => {
      try {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const rawStatus: Record<string, any> = JSON.parse(statusJSON);
        
        // Normalize field names (Go uses capital case, TypeScript uses camelCase)
        const normalizedStatus: SyncStatus = {
          status: (rawStatus.Status || rawStatus.status || null) as SyncStatus["status"],
          message: rawStatus.Message || rawStatus.message || "",
          conversationId: rawStatus.ConversationID || rawStatus.ConversationId || rawStatus.conversationId,
          progress: rawStatus.Progress !== undefined ? rawStatus.Progress : (rawStatus.progress !== undefined ? rawStatus.progress : -1),
        };
        
        // If we receive a "completed" status, always clear any pending status
        // and set up the auto-hide timeout
        if (normalizedStatus.status === "completed") {
          // Mark that we've received a completed status
          hasCompletedRef.current = true;
          
          // Clear any existing timeout
          if (timeoutId) {
            clearTimeout(timeoutId);
            timeoutId = null;
          }
          
          // Set the completed status
          setSyncStatus(normalizedStatus);
          
          // Auto-hide after 2 seconds
          timeoutId = setTimeout(() => {
            setSyncStatus(null);
            hasCompletedRef.current = false; // Reset for next sync cycle
            timeoutId = null;
          }, 2000);
          return; // Don't process further
        }
        
        // For error status
        if (normalizedStatus.status === "error") {
          // Reset completed flag on error
          hasCompletedRef.current = false;
          
          // Clear any existing timeout
          if (timeoutId) {
            clearTimeout(timeoutId);
            timeoutId = null;
          }
          
          setSyncStatus(normalizedStatus);
          
          // Show error for 5 seconds
          timeoutId = setTimeout(() => {
            setSyncStatus(null);
            timeoutId = null;
          }, 5000);
          return; // Don't process further
        }
        
        // For other statuses (fetching_contacts, fetching_history, fetching_avatars)
        // If we've already received a "completed" status, ignore these events
        // They are likely late events from the previous sync cycle
        // Exception: fetching_avatars can come after completed, so we allow it
        if (hasCompletedRef.current && normalizedStatus.status !== "fetching_avatars") {
          return; // Ignore late events (except avatars which can load after sync)
        }
        
        // Clear any existing timeout (but don't auto-hide for these)
        if (timeoutId) {
          clearTimeout(timeoutId);
          timeoutId = null;
        }
        
        setSyncStatus(normalizedStatus);
      } catch (error) {
        console.error("Failed to parse sync status:", error);
      }
    });

    return () => {
      if (timeoutId) {
        clearTimeout(timeoutId);
      }
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, []); // Empty deps - callback will always use latest ref value

  // Always show if there's a status, even if completed (will auto-hide after 3s)
  if (!syncStatus) {
    return null;
  }

  // Don't show spinner for completed status
  const showSpinner = syncStatus.status !== "completed" && syncStatus.status !== "error";

  // Get display text - use the message from the status, which already contains the step
  // Translate backend messages
  const rawMessage = syncStatus.message || t("synchronizing");
  const displayText = translateBackendMessage(rawMessage);

  return (
    <div className="h-12 border-t bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60 flex items-center px-4 gap-2 text-sm z-50">
      {showSpinner && <Loader2 className="h-4 w-4 animate-spin flex-shrink-0 text-primary" />}
      <span className="flex-1 truncate font-medium text-foreground">{displayText}</span>
      {syncStatus.progress >= 0 && (
        <span className="ml-auto text-xs flex-shrink-0 text-muted-foreground">
          {syncStatus.progress}%
        </span>
      )}
    </div>
  );
}

