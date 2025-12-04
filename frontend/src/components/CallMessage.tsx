import { Clock, Phone, PhoneMissed, Video, VideoOff } from "lucide-react";

import type { models } from "../../wailsjs/go/models";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

interface CallMessageProps {
  message: models.Message;
  layout: "bubble" | "irc";
  isGroup?: boolean;
}

// Format duration in seconds to human-readable format (e.g., "5m 30s", "1h 2m")
function formatDuration(seconds: number): string {
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const secs = seconds % 60;
  
  if (hours > 0) {
    if (minutes > 0) {
      return `${hours}h ${minutes}m`;
    }
    return `${hours}h`;
  }
  if (secs > 0) {
    return `${minutes}m ${secs}s`;
  }
  return `${minutes}m`;
}

export function CallMessage({ message, layout, isGroup = false }: CallMessageProps) {
  const { t } = useTranslation();

  // Parse participants JSON if available
  const participants = useMemo(() => {
    if (!message.callParticipants) {
      return [];
    }
    try {
      return JSON.parse(message.callParticipants) as string[];
    } catch {
      return [];
    }
  }, [message.callParticipants]);

  // Determine call type and icon
  const getCallInfo = () => {
    const callType = message.callType || "";
    const hasSummary = message.callDurationSecs != null || message.callOutcome || participants.length > 0;
    
    // If we have call summary, show more detailed information
    if (hasSummary) {
      const isVideo = message.callIsVideo;
      const duration = message.callDurationSecs;
      const outcome = message.callOutcome;
      
      // Determine outcome text
      let outcomeText = "";
      if (outcome === "CONNECTED") {
        if (duration != null && duration > 0) {
          outcomeText = t("call.connected", { duration: formatDuration(duration) });
        } else {
          outcomeText = t("call.connectedShort");
        }
      } else if (outcome === "MISSED") {
        outcomeText = isVideo 
          ? (isGroup ? t("call.missedGroupVideo") : t("call.missedVideo"))
          : (isGroup ? t("call.missedGroupVoice") : t("call.missedVoice"));
      } else if (outcome === "FAILED") {
        outcomeText = t("call.failed");
      } else if (outcome === "REJECTED") {
        outcomeText = t("call.rejected");
      } else {
        // Fallback to call type
        if (callType.includes("missed")) {
          outcomeText = isVideo 
            ? (isGroup ? t("call.missedGroupVideo") : t("call.missedVideo"))
            : (isGroup ? t("call.missedGroupVoice") : t("call.missedVoice"));
        } else {
          outcomeText = t("call.missedVoice");
        }
      }
      
      return {
        icon: isVideo ? Video : Phone,
        text: outcomeText,
        duration: duration != null ? formatDuration(duration) : null,
        participantCount: participants.length,
      };
    }
    
    // No summary available, use basic call type
    if (callType.includes("missed")) {
      if (callType.includes("video")) {
        return {
          icon: VideoOff,
          text: isGroup ? t("call.missedGroupVideo") : t("call.missedVideo"),
          duration: null,
          participantCount: 0,
        };
      } else {
        return {
          icon: PhoneMissed,
          text: isGroup ? t("call.missedGroupVoice") : t("call.missedVoice"),
          duration: null,
          participantCount: 0,
        };
      }
    } else if (callType === "scheduled_start") {
      return {
        icon: Phone,
        text: t("call.scheduledStart"),
        duration: null,
        participantCount: 0,
      };
    } else if (callType === "scheduled_cancel") {
      return {
        icon: Phone,
        text: t("call.scheduledCancel"),
        duration: null,
        participantCount: 0,
      };
    } else if (callType === "linked_group_start") {
      return {
        icon: Video,
        text: t("call.linkedGroupStart"),
        duration: null,
        participantCount: 0,
      };
    }
    
    // Default fallback
    return {
      icon: Phone,
      text: t("call.missedVoice"),
      duration: null,
      participantCount: 0,
    };
  };

  const callInfo = getCallInfo();
  const Icon = callInfo.icon;
  const hasSummary = message.callDurationSecs != null || message.callOutcome || participants.length > 0;

  if (layout === "bubble") {
    // Bubble layout: centered, visually distinct bubble
    return (
      <div className="flex justify-center my-2">
        <div className="flex flex-col items-center gap-1 px-4 py-2 rounded-full bg-muted/50 border border-border/50 text-muted-foreground text-sm">
          <div className="flex items-center gap-2">
            <Icon className="h-4 w-4" />
            <span>{callInfo.text}</span>
          </div>
          {hasSummary && (
            <div className="flex items-center gap-3 text-xs opacity-80">
              {callInfo.duration && (
                <div className="flex items-center gap-1">
                  <Clock className="h-3 w-3" />
                  <span>{callInfo.duration}</span>
                </div>
              )}
              {callInfo.participantCount > 0 && isGroup && (
                <span>{t("call.participants", { count: callInfo.participantCount })}</span>
              )}
            </div>
          )}
        </div>
      </div>
    );
  } else {
    // IRC layout: information message style (like system messages in IRC)
    return (
      <div className="flex flex-col gap-1 px-2 py-1 text-xs text-muted-foreground italic">
        <div className="flex items-center gap-2">
          <Icon className="h-3 w-3" />
          <span className="text-muted-foreground/80">*** {callInfo.text}</span>
        </div>
        {hasSummary && (
          <div className="flex items-center gap-3 ml-5 text-muted-foreground/70">
            {callInfo.duration && (
              <div className="flex items-center gap-1">
                <Clock className="h-3 w-3" />
                <span>{callInfo.duration}</span>
              </div>
            )}
            {callInfo.participantCount > 0 && isGroup && (
              <span>{t("call.participants", { count: callInfo.participantCount })}</span>
            )}
          </div>
        )}
      </div>
    );
  }
}

