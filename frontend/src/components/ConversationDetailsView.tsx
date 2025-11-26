import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { X } from "lucide-react";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useMemo, useState } from "react";
import { GetMessagesForConversation, GetContactAliases, SetContactAlias } from "../../wailsjs/go/main/App";
import { useSuspenseQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { translateBackendMessage } from "@/lib/i18n-helpers";

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
    // Format phone number: add spaces every 2 digits (French format)
    if (phoneNumber.startsWith("33") && phoneNumber.length >= 10) {
      const countryCode = phoneNumber.substring(0, 2);
      const rest = phoneNumber.substring(2);
      const formatted = `+${countryCode} ${rest.substring(0, 1)} ${rest.substring(1, 3)} ${rest.substring(3, 5)} ${rest.substring(5, 7)} ${rest.substring(7)}`;
      return formatted;
    } else {
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

  const queryClient = useQueryClient();
  const { data: messages } = useSuspenseQuery<models.Message[], Error>({
    queryKey: ["messages", selectedConversation.id],
    queryFn: () =>
      fetchMessages(selectedConversation.linkedAccounts[0].userId),
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
      const msgTime = new Date(msg.timestamp);
      
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

  return (
    <div className="flex flex-col h-full">
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
              Participants ({participants.length})
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
            title="Click to edit name"
          >
            <p className="font-medium text-sm truncate">{displayName}</p>
            {alias && (
              <span className="text-xs text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity">
                (custom)
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

