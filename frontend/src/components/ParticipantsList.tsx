import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { X } from "lucide-react";
import { GetGroupParticipants, GetParticipantNames, SetContactAlias } from "../../wailsjs/go/main/App";
import { useEffect, useMemo, useState } from "react";
import { useQueryClient, useSuspenseQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { models } from "../../wailsjs/go/models";
import { timeToDate } from "@/lib/utils";
import { useAppStore } from "@/lib/store";
import { usePresenceStore } from "@/lib/presenceStore";
import { useTranslation } from "react-i18next";

interface ParticipantsListProps {
  conversationId: string;
  messages: models.Message[];
  selectedConversation: models.MetaContact;
  aliases: Record<string, string>;
  onAvatarClick: (avatarUrl: string | undefined, displayName: string) => void;
  onParticipantsCountChange?: (count: number) => void;
}

// Fetch function that loads both group participants and their names
async function fetchParticipantsData(conversationId: string): Promise<{
  groupParticipants: models.GroupParticipant[];
  participantNames: Record<string, string>;
}> {
  let groupParticipants;
  try {
    groupParticipants = await GetGroupParticipants(conversationId);
  } catch (err) {
    console.error("Failed to get group participants:", err);
    return { groupParticipants: [], participantNames: {} };
  }

  if (!groupParticipants || groupParticipants.length === 0) {
    return { groupParticipants: [], participantNames: {} };
  }

  const ids = groupParticipants.map((p) => p.userId);
  try {
    const participantNames = await GetParticipantNames(ids);
    return { groupParticipants, participantNames: participantNames || {} };
  } catch (err) {
    console.error("Failed to get participant names:", err);
    return { groupParticipants, participantNames: {} };
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
    .replace(/^[a-z]+-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

export function ParticipantsList({
  conversationId,
  messages,
  selectedConversation,
  aliases,
  onAvatarClick,
  onParticipantsCountChange,
}: ParticipantsListProps) {
  const { t } = useTranslation();
  const presenceMap = usePresenceStore((state) => state.presenceMap);
  const metaContacts = useAppStore((state) => state.metaContacts);
  const queryClient = useQueryClient();

  // Use Suspense query to load participants data
  const { data: participantsData } = useSuspenseQuery<{
    groupParticipants: models.GroupParticipant[];
    participantNames: Record<string, string>;
  }, Error>({
    queryKey: ["participantsData", conversationId],
    queryFn: () => fetchParticipantsData(conversationId),
  });

  const { groupParticipants: groupParticipantsData, participantNames } = participantsData;

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

  // Notify parent of participant count change
  useEffect(() => {
    if (onParticipantsCountChange) {
      onParticipantsCountChange(participants.length);
    }
  }, [participants.length, onParticipantsCountChange]);

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

  return (
    <div className="space-y-3">
      {participants.map((participant) => {
        const displayName = getDisplayNameWithAlias(
          participant.senderName,
          participant.senderId,
          participant.isFromMe
        );

        // WhatsApp presence check via presenceMap
        const isOnlinePresence = presenceMap[participant.senderId] === true;
        let presenceMatch = isOnlinePresence;
        if (!presenceMatch && participant.senderId.includes("@")) {
          const jidPhone = participant.senderId.split("@")[0];
          for (const [lid, online] of Object.entries(presenceMap)) {
            if (online && lid.endsWith("@lid")) {
              const lidPhone = lid.replace(/@lid$/, "").replace(/:\d+$/, "");
              if (jidPhone === lidPhone) {
                presenceMatch = true;
                break;
              }
            }
          }
        }

        // Find the participant's own linked account for provider status/avatar.
        // First try direct match in selectedConversation (works for DMs).
        // Fall back to metaContacts store (works for channel participants).
        const participantLinkedAccount = (() => {
          const direct = selectedConversation.linkedAccounts?.find(
            acc => acc.userId === participant.senderId
          );
          if (direct) return direct;
          // In a DM, some providers key the linked account by conversation ID
          // while messages identify the peer with a separate user ID. The sole
          // non-group account still represents that peer's generic status.
          if (!participant.isFromMe) {
            const directMessageAccount = selectedConversation.linkedAccounts?.find(
              acc => !acc.isGroup
            );
            if (directMessageAccount) return directMessageAccount;
          }
          for (const contact of metaContacts) {
            const acc = contact.linkedAccounts?.find(a => a.userId === participant.senderId);
            if (acc) return acc;
          }
          return null;
        })();

        const linkedAccountStatus = (participantLinkedAccount?.status && participantLinkedAccount.status !== "offline")
          ? participantLinkedAccount.status
          : null;
        const avatarUrl = participantLinkedAccount?.avatarUrl || participant.senderAvatarUrl;

        // Mirror ContactList logic: non-offline provider status takes priority, then presenceMap
        const effectiveStatus = linkedAccountStatus || (presenceMatch ? "online" : "offline");

        return (
          <ParticipantItem
            key={participant.senderId}
            participant={{
              ...participant,
              senderAvatarUrl: avatarUrl,
            }}
            displayName={displayName}
            status={effectiveStatus}
            alias={aliases[participant.senderId]}
            onAvatarClick={onAvatarClick}
            onAliasChange={async (newAlias: string) => {
              await SetContactAlias(participant.senderId, newAlias);
              queryClient.invalidateQueries({ queryKey: ["contactAliases"] });
              queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
            }}
          />
        );
      })}
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

  // Render the status text row
  const renderStatusText = () => {
    if (!status || status === "offline") {
      return (
        <>
          <span className="h-2 w-2 rounded-full bg-gray-500" />
          <p className="text-xs text-muted-foreground">{t("offline")}</p>
        </>
      );
    }
    const colorMap: Record<string, string> = {
      online: "bg-green-500",
      meeting: "bg-blue-500",
      away: "bg-yellow-500",
      busy: "bg-red-500",
      dnd: "bg-red-500",
      holiday: "bg-purple-500",
    };
    const labelMap: Record<string, string> = {
      online: t("online"),
      meeting: t("meeting") || "In a meeting",
      away: t("away") || "Away",
      busy: t("busy") || "Busy",
      dnd: t("dnd") || "Do not disturb",
      holiday: t("holiday") || "Holiday",
    };
    const dotColor = colorMap[status] ?? "bg-gray-500";
    const label = labelMap[status] ?? status;
    return (
      <>
        <span className={`h-2 w-2 rounded-full ${dotColor}`} />
        <p className="text-xs text-muted-foreground">{label}</p>
      </>
    );
  };

  if (participant.isFromMe) {
    // Don't show status for current user
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
      <div className="shrink-0">
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
      </div>
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
          {renderStatusText()}
        </div>
      </div>
    </div>
  );
}
