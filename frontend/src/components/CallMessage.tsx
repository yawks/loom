import { Clock, ExternalLink, Phone, PhoneOutgoing, Video, VideoOff, X } from "lucide-react";

import type { models } from "../../wailsjs/go/models";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";

// Custom icon component for missed calls: Phone with X overlay
const PhoneWithX = ({ className }: { className?: string }) => (
  <span className="relative inline-block">
    <Phone className={className} />
    <X className={`absolute top-0 left-0 ${className}`} strokeWidth={3} />
  </span>
);

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
    const isOutgoing = callType.startsWith("outgoing_");
    const isVideo = message.callIsVideo || callType.includes("video");
    const duration = message.callDurationSecs;
    const outcome = message.callOutcome;
    const hasSummary = duration != null || outcome || participants.length > 0;

    if (callType === "call_ended") {
      return {
        icon: isVideo ? Video : Phone,
        text: t("call.ended"),
        duration: duration != null ? formatDuration(duration) : null,
        participantCount: participants.length,
      };
    }

    // Outgoing calls
    if (isOutgoing) {
      const baseIcon = isVideo ? Video : PhoneOutgoing;

      if (outcome === "CONNECTED" && duration != null && duration > 0) {
        return {
          icon: baseIcon,
          text: t("call.outgoingConnected", { duration: formatDuration(duration) }),
          duration: null, // already in text
          participantCount: participants.length,
        };
      }
      if (outcome === "CONNECTED") {
        return {
          icon: baseIcon,
          text: t("call.connectedShort"),
          duration: null,
          participantCount: participants.length,
        };
      }
      if (outcome === "MISSED") {
        return {
          icon: isVideo ? VideoOff : PhoneOutgoing,
          text: t("call.outgoingNoAnswer"),
          duration: null,
          participantCount: participants.length,
        };
      }
      if (outcome === "REJECTED") {
        return {
          icon: PhoneWithX,
          text: t("call.rejected"),
          duration: null,
          participantCount: participants.length,
        };
      }
      if (outcome === "FAILED") {
        return {
          icon: PhoneWithX,
          text: t("call.failed"),
          duration: null,
          participantCount: participants.length,
        };
      }
      // No outcome yet (live / just placed)
      const textKey = isVideo
        ? (callType.includes("group") ? "call.outgoingGroupVideo" : "call.outgoingVideo")
        : (callType.includes("group") ? "call.outgoingGroupVoice" : "call.outgoingVoice");
      return {
        icon: baseIcon,
        text: t(textKey),
        duration: duration != null ? formatDuration(duration) : null,
        participantCount: participants.length,
      };
    }

    // Incoming calls — use hasSummary to pick between summary and basic display
    if (hasSummary) {
      let outcomeText = "";
      let iconComponent: React.ComponentType<{ className?: string }> = Phone;

      if (outcome === "CONNECTED") {
        if (duration != null && duration > 0) {
          outcomeText = t("call.connected", { duration: formatDuration(duration) });
        } else {
          outcomeText = t("call.connectedShort");
        }
        iconComponent = isVideo ? Video : Phone;
      } else if (outcome === "MISSED") {
        outcomeText = isVideo
          ? (isGroup ? t("call.missedGroupVideo") : t("call.missedVideo"))
          : (isGroup ? t("call.missedGroupVoice") : t("call.missedVoice"));
        iconComponent = isVideo ? VideoOff : PhoneWithX;
      } else if (outcome === "FAILED") {
        outcomeText = t("call.failed");
        iconComponent = PhoneWithX;
      } else if (outcome === "REJECTED") {
        outcomeText = t("call.rejected");
        iconComponent = PhoneWithX;
      } else {
        // Fallback to call type
        if (callType.includes("missed")) {
          outcomeText = isVideo
            ? (isGroup ? t("call.missedGroupVideo") : t("call.missedVideo"))
            : (isGroup ? t("call.missedGroupVoice") : t("call.missedVoice"));
          iconComponent = isVideo ? VideoOff : PhoneWithX;
        } else {
          outcomeText = t("call.missedVoice");
          iconComponent = PhoneWithX;
        }
      }

      return {
        icon: iconComponent,
        text: outcomeText,
        duration: duration != null ? formatDuration(duration) : null,
        participantCount: participants.length,
      };
    }

    // No summary — use basic call type
    if (callType === "incoming_call" || callType === "incoming_group_call") {
      return {
        icon: Phone,
        text: callType === "incoming_group_call" ? t("call.incomingGroupCall") : t("call.incomingCall"),
        duration: null,
        participantCount: 0,
      };
    }
    if (callType.includes("missed")) {
      return {
        icon: callType.includes("video") ? VideoOff : PhoneWithX,
        text: isVideo
          ? (isGroup ? t("call.missedGroupVideo") : t("call.missedVideo"))
          : (isGroup ? t("call.missedGroupVoice") : t("call.missedVoice")),
        duration: null,
        participantCount: 0,
      };
    }
    if (callType === "scheduled_start") {
      return { icon: Phone, text: t("call.scheduledStart"), duration: null, participantCount: 0 };
    }
    if (callType === "scheduled_cancel") {
      return { icon: Phone, text: t("call.scheduledCancel"), duration: null, participantCount: 0 };
    }
    if (callType === "linked_group_start") {
      return { icon: Video, text: t("call.linkedGroupStart"), duration: null, participantCount: 0 };
    }

    // Default fallback
    return { icon: Phone, text: t("call.missedVoice"), duration: null, participantCount: 0 };
  };

  const callInfo = getCallInfo();
  const Icon = callInfo.icon;
  const hasDuration = callInfo.duration != null;
  const hasParticipants = callInfo.participantCount > 0 && isGroup;

  if (layout === "bubble") {
    return (
      <div className="flex justify-center my-2">
        <div className="flex flex-col items-center gap-1 px-4 py-2 rounded-full bg-muted/50 border border-border/50 text-muted-foreground text-sm">
          <div className="flex items-center gap-2">
            <Icon className="h-4 w-4" />
            <span>{callInfo.text}</span>
            {message.callUrl && (
              <button
                onClick={() => BrowserOpenURL(message.callUrl!)}
                className="flex items-center gap-1 text-xs text-primary hover:underline"
              >
                <ExternalLink className="h-3 w-3" />
                {t("call.join")}
              </button>
            )}
          </div>
          {(hasDuration || hasParticipants) && (
            <div className="flex items-center gap-3 text-xs opacity-80">
              {hasDuration && (
                <div className="flex items-center gap-1">
                  <Clock className="h-3 w-3" />
                  <span>{callInfo.duration}</span>
                </div>
              )}
              {hasParticipants && (
                <span>{t("call.participants", { count: callInfo.participantCount })}</span>
              )}
            </div>
          )}
        </div>
      </div>
    );
  } else {
    // IRC layout
    return (
      <div className="flex flex-col gap-1 px-2 py-1 text-xs text-muted-foreground italic">
        <div className="flex items-center gap-2">
          <Icon className="h-3 w-3" />
          <span className="text-muted-foreground/80">*** {callInfo.text}</span>
          {message.callUrl && (
            <button
              onClick={() => BrowserOpenURL(message.callUrl!)}
              className="flex items-center gap-1 not-italic text-primary hover:underline"
            >
              <ExternalLink className="h-3 w-3" />
              {t("call.join")}
            </button>
          )}
        </div>
        {(hasDuration || hasParticipants) && (
          <div className="flex items-center gap-3 ml-5 text-muted-foreground/70">
            {hasDuration && (
              <div className="flex items-center gap-1">
                <Clock className="h-3 w-3" />
                <span>{callInfo.duration}</span>
              </div>
            )}
            {hasParticipants && (
              <span>{t("call.participants", { count: callInfo.participantCount })}</span>
            )}
          </div>
        )}
      </div>
    );
  }
}
