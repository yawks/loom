import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Loader2, Plus, ShieldCheck, ShieldMinus, UserMinus, X } from "lucide-react";
import { AddGroupParticipants, DemoteGroupAdmins, GetGroupParticipants, GetParticipantNames, PromoteGroupAdmins, SearchProviderContacts, RemoveGroupParticipants, SetContactAliasForConversation } from "../../wailsjs/go/main/App";
import { useEffect, useMemo, useState } from "react";
import { useQueryClient, useSuspenseQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
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
  onAvatarClick: (avatarUrl: string | undefined, displayName: string, userId: string, status: string) => void;
  onParticipantsCountChange?: (count: number) => void;
  providerInstanceId: string;
  isGroup: boolean;
  canAddMembers: boolean;
  canRemoveMembers: boolean;
  canManageAdmins: boolean;
}

// Fetch function that loads both group participants and their names
async function fetchParticipantsData(conversationId: string, isGroup: boolean): Promise<{
  groupParticipants: models.GroupParticipant[];
  participantNames: Record<string, string>;
}> {
  // Direct conversations derive their participants from messages. Calling the
  // provider's group endpoint for them is both unnecessary and an error for
  // providers such as WhatsApp.
  if (!isGroup) {
    return { groupParticipants: [], participantNames: {} };
  }

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
  providerInstanceId,
  isGroup,
  canAddMembers,
  canRemoveMembers,
  canManageAdmins,
}: ParticipantsListProps) {
  const { t } = useTranslation();
  const presenceMap = usePresenceStore((state) => state.presenceMap);
  const metaContacts = useAppStore((state) => state.metaContacts);
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [search, setSearch] = useState("");
  const [candidates, setCandidates] = useState<models.MetaContact[]>([]);
  const [isSearching, setIsSearching] = useState(false);
  const [mutationError, setMutationError] = useState("");
  const [participantToRemove, setParticipantToRemove] = useState<{ id: string; name: string } | null>(null);
  const [isRemoving, setIsRemoving] = useState(false);

  // Use Suspense query to load participants data
  const { data: participantsData } = useSuspenseQuery<{
    groupParticipants: models.GroupParticipant[];
    participantNames: Record<string, string>;
  }, Error>({
    queryKey: ["participantsData", conversationId, isGroup],
    queryFn: () => fetchParticipantsData(conversationId, isGroup),
    refetchInterval: 15000,
  });

  const { groupParticipants: groupParticipantsData, participantNames } = participantsData;

  useEffect(() => {
    if (!adding || !providerInstanceId) return;
    let cancelled = false;
    setIsSearching(true);
    const timer = window.setTimeout(() => {
      SearchProviderContacts(providerInstanceId, search.trim())
        .then((items) => { if (!cancelled) setCandidates(items ?? []); })
        .catch((error) => { if (!cancelled) setMutationError(String(error)); })
        .finally(() => { if (!cancelled) setIsSearching(false); });
    }, 250);
    return () => { cancelled = true; window.clearTimeout(timer); };
  }, [adding, providerInstanceId, search]);

  const refreshParticipants = async () => {
    await queryClient.invalidateQueries({ queryKey: ["participantsData", conversationId] });
  };

  const addParticipant = async (contact: models.MetaContact) => {
    const account = contact.linkedAccounts?.find((item) => item.providerInstanceId === providerInstanceId);
    if (!account) return;
    setMutationError("");
    try {
      await AddGroupParticipants(conversationId, [account.userId]);
      await refreshParticipants();
      setAdding(false);
      setSearch("");
    } catch (error) {
      setMutationError(String(error));
    }
  };

  const removeParticipant = async (participantID: string) => {
    setMutationError("");
    setIsRemoving(true);
    try {
      await RemoveGroupParticipants(conversationId, [participantID]);
      await refreshParticipants();
      setParticipantToRemove(null);
    } catch (error) {
      setMutationError(String(error));
    } finally {
      setIsRemoving(false);
    }
  };

  const setParticipantAdmin = async (participantID: string, makeAdmin: boolean) => {
    setMutationError("");
    try {
      if (makeAdmin) await PromoteGroupAdmins(conversationId, [participantID]);
      else await DemoteGroupAdmins(conversationId, [participantID]);
      await refreshParticipants();
    } catch (error) {
      setMutationError(String(error));
    }
  };

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
            isFromMe: participant.isSelf || (currentUserId ? participant.userId === currentUserId : false),
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
            existing.isFromMe = participant.isSelf || (currentUserId ? participant.userId === currentUserId : false);
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
      {canAddMembers && (
        <div className="space-y-2">
          <Button variant="outline" size="sm" className="w-full" onClick={() => setAdding((value) => !value)}>
            <Plus className="h-4 w-4 mr-2" />{t("add_group_member")}
          </Button>
          {adding && (
            <div className="rounded-lg border p-2 space-y-2">
              <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t("search_group_members")} autoFocus />
              <div className="max-h-48 overflow-y-auto">
                {isSearching && <Loader2 className="h-4 w-4 animate-spin mx-auto my-3" />}
                {!isSearching && candidates.filter((contact) => {
                  const id = contact.linkedAccounts?.find((item) => item.providerInstanceId === providerInstanceId)?.userId;
                  return id && !groupParticipantsData.some((participant) => participant.userId === id);
                }).map((contact) => (
                  <button key={contact.id} className="w-full text-left rounded p-2 text-sm hover:bg-muted" onClick={() => addParticipant(contact)}>
                    {contact.displayName}
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
      {mutationError && <p className="text-sm text-destructive">{t("group_member_update_error")}: {mutationError}</p>}
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
              await SetContactAliasForConversation(participant.senderId, conversationId, newAlias);
              queryClient.invalidateQueries({ queryKey: ["contactAliases"] });
              queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
            }}
            onRemove={canRemoveMembers && !participant.isFromMe ? () => setParticipantToRemove({ id: participant.senderId, name: displayName }) : undefined}
            onToggleAdmin={canManageAdmins && !participant.isFromMe ? () => void setParticipantAdmin(participant.senderId, !participant.isAdmin) : undefined}
          />
        );
      })}
      <AlertDialog open={participantToRemove !== null} onOpenChange={(open) => { if (!open && !isRemoving) setParticipantToRemove(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("remove_group_member")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("remove_group_member_confirm", { name: participantToRemove?.name ?? "" })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isRemoving}>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              disabled={isRemoving || !participantToRemove}
              onClick={(event) => {
                event.preventDefault();
                if (participantToRemove) void removeParticipant(participantToRemove.id);
              }}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {isRemoving ? t("removing_group_member") : t("remove_group_member")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
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
  onAvatarClick: (avatarUrl: string | undefined, displayName: string, userId: string, status: string) => void;
  onAliasChange: (newAlias: string) => Promise<void>;
  onRemove?: () => void;
  onToggleAdmin?: () => void;
}

function ParticipantItem({
  participant,
  displayName,
  status,
  alias,
  onAvatarClick,
  onAliasChange,
  onRemove,
  onToggleAdmin,
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
          onClick={() => onAvatarClick(participant.senderAvatarUrl, displayName, participant.senderId, status)}
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
          onClick={() => onAvatarClick(participant.senderAvatarUrl, displayName, participant.senderId, status)}
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
      {onRemove && (
        <Button variant="ghost" size="icon" className="shrink-0 text-muted-foreground hover:text-destructive" onClick={onRemove} title={t("remove_group_member")}>
          <UserMinus className="h-4 w-4" />
        </Button>
      )}
      {onToggleAdmin && (
        <Button variant="ghost" size="icon" className="shrink-0 text-muted-foreground" onClick={onToggleAdmin} title={participant.isAdmin ? t("demote_group_admin") : t("promote_group_admin")}>
          {participant.isAdmin ? <ShieldMinus className="h-4 w-4" /> : <ShieldCheck className="h-4 w-4" />}
        </Button>
      )}
    </div>
  );
}
