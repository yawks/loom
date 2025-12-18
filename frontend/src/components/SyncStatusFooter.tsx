import { useEffect, useState, useRef } from "react";
import { useTranslation } from "react-i18next";

import { EventsOn } from "../../wailsjs/runtime/runtime";
import { Loader2, CheckCircle2, AlertCircle, XCircle } from "lucide-react";
import { translateBackendMessage } from "@/lib/i18n-helpers";
import { cn } from "@/lib/utils";

interface SyncStatus {
  status: "fetching_contacts" | "fetching_history" | "fetching_avatars" | "completed" | "error" | null;
  message: string;
  conversationId?: string;
  progress: number;
}

export function SyncStatusFooter() {
  const { t } = useTranslation();
  const [syncStatus, setSyncStatus] = useState<SyncStatus | null>(null);
  const [isVisible, setIsVisible] = useState(false);
  const hasCompletedRef = useRef(false);

  // Show stop button if sync takes too long
  const [showStopButton, setShowStopButton] = useState(false);
  const stopTimerRef = useRef<any>(null);

  // Parse status JSON safely
  const getParsedStatus = (jsonStatus: string | null): SyncStatus | null => {
    if (!jsonStatus) return null;
    try {
      const parsed = JSON.parse(jsonStatus);
      // Normalize casing
      return {
        status: (parsed.Status || parsed.status || "").toLowerCase(),
        message: parsed.Message || parsed.message || "",
        conversationId: parsed.ConversationID || parsed.ConversationId || parsed.conversationId,
        progress: parsed.Progress !== undefined ? parsed.Progress : (parsed.progress !== undefined ? parsed.progress : 0)
      };
    } catch (e) {
      console.error("Failed to parse sync status:", e);
      return null;
    }
  };

  useEffect(() => {
    let timeoutId: any = null;

    const unsubscribe = EventsOn("sync-status", (statusJSON: string) => {
      // Parse the status
      const normalizedStatus = getParsedStatus(statusJSON);

      if (normalizedStatus) {
        // Check if completed
        if (normalizedStatus.status === "completed") {
          hasCompletedRef.current = true;
          // Auto-hide after 3 seconds
          if (timeoutId) clearTimeout(timeoutId);
          timeoutId = setTimeout(() => {
            setIsVisible(false);
            setSyncStatus(null);
            timeoutId = null;
          }, 3000);
        } else if (normalizedStatus.status === "error") {
          // Auto-hide error after 5 seconds
          if (timeoutId) clearTimeout(timeoutId);
          timeoutId = setTimeout(() => {
            setIsVisible(false);
            setSyncStatus(null);
            timeoutId = null;
          }, 5000);
        } else {
          // For active syncing states (fetching_contacts, fetching_history, etc.)
          // We allow these updates even if we received a completed event before
          // This handles cases where history continues syncing after initial load

          if (timeoutId) {
            clearTimeout(timeoutId);
            timeoutId = null;
          }
        }

        // Always show for new events (unless currently hiding)
        setIsVisible(true);
        setSyncStatus(normalizedStatus);
      }
    });

    return () => {
      if (unsubscribe) unsubscribe();
      if (timeoutId) clearTimeout(timeoutId);
    };
  }, []);

  // Timer for stop button
  useEffect(() => {
    // Reset timer on status change
    if (stopTimerRef.current) {
      clearTimeout(stopTimerRef.current);
      stopTimerRef.current = null;
    }

    // Only start timer if we are in a syncing state
    if (syncStatus && syncStatus.status !== "completed" && syncStatus.status !== "error") {
      setShowStopButton(false);
      stopTimerRef.current = setTimeout(() => {
        setShowStopButton(true);
      }, 15000); // Show stop button after 15 seconds
    } else {
      setShowStopButton(false);
    }

    return () => {
      if (stopTimerRef.current) {
        clearTimeout(stopTimerRef.current);
      }
    };
  }, [syncStatus]);

  const handleStopSync = async () => {
    try {
      // Call backend to force completion
      // Using any cast to avoid type errors if bindings aren't regenerated yet
      if ((window as any).go && (window as any).go.main && (window as any).go.main.App) {
        await (window as any).go.main.App.ForceSyncCompletion();
      } else {
        console.error("Wails runtime not available");
        // Fallback: manually set completed state locally if backend call fails
        setSyncStatus({ status: "completed", message: "Stopped by user", progress: 100 });
      }
    } catch (err) {
      console.error("Failed to stop sync:", err);
    }
  };

  const getMessage = (status: SyncStatus) => {
    // Check if we have a translation key
    let key = "";
    switch (status.status) {
      case "fetching_contacts":
        key = "sync.steps.fetching_contacts";
        break;
      case "fetching_history":
        key = "sync.steps.fetching_history";
        break;
      case "fetching_avatars":
        key = "sync.steps.fetching_avatars";
        break;
      case "completed":
        key = "sync.completed";
        break;
      case "error":
        return status.message; // Use raw message for errors usually
      default:
        // Try to translate the message if it looks like a key, otherwise return raw
        return translateBackendMessage(status.message);
    }

    return t(key);
  };

  if (!syncStatus || !isVisible) return null;

  const isCompleted = syncStatus.status === "completed";
  const isError = syncStatus.status === "error";

  return (
    <div className="bg-muted/50 border-t border-border p-2 text-xs flex items-center justify-between animate-in slide-in-from-bottom-2 duration-300">
      <div className="flex items-center space-x-2 truncate">
        {isCompleted ? (
          <CheckCircle2 className="h-3 w-3 text-green-500 shrink-0" />
        ) : isError ? (
          <AlertCircle className="h-3 w-3 text-destructive shrink-0" />
        ) : (
          <Loader2 className="h-3 w-3 animate-spin text-muted-foreground shrink-0" />
        )}
        <span className={cn("truncate", isError && "text-destructive")}>
          {getMessage(syncStatus)}
        </span>
      </div>

      {showStopButton && !isCompleted && !isError && (
        <button
          onClick={handleStopSync}
          className="ml-2 px-2 py-0.5 bg-background border border-border rounded text-[10px] hover:bg-muted transition-colors flex items-center shadow-sm"
        >
          <XCircle className="h-3 w-3 mr-1 text-muted-foreground" />
          Stop
        </button>
      )}

      {!isCompleted && !isError && !showStopButton && syncStatus.progress > 0 && (
        <span className="text-muted-foreground ml-2 shrink-0 font-mono">
          {syncStatus.progress}%
        </span>
      )}
    </div>
  );
}
