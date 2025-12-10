import { ArrowDownAZ, Calendar, Clock, Phone, Plus, Smile } from "lucide-react";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { GetMessagesForConversation, GetMetaContacts } from "../../wailsjs/go/main/App";
import { useEffect, useMemo, useState } from "react";
import { useQueries, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { MessageText } from "./MessageText";
import { NewConversationModal } from "./NewConversationModal";
import { SlackEmoji } from "./SlackEmoji";
import { cn } from "@/lib/utils";
import { getContactStatusEmoji } from "@/lib/statusEmoji";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { usePresenceStore } from "@/lib/presenceStore";
import { useSortedContacts } from "@/hooks/useSortedContacts";
import { useTranslation } from "react-i18next";
import { useTypingStore } from "@/lib/typingStore";

type SortOption = "alphabetical" | "last_message";


// Wrapper function to use Wails with React Query's suspense mode
const fetchMetaContacts = async () => {
  return GetMetaContacts();
};

export function ContactList() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const selectedContact = useAppStore((state) => state.selectedContact);
  const setSelectedContact = useAppStore((state) => state.setSelectedContact);
  const setMetaContacts = useAppStore((state) => state.setMetaContacts);
  const [sortBy, setSortBy] = useState<SortOption>("last_message");
  // Use object directly - Zustand handles object reactivity better than Map
  // Use a selector that returns a serialized version to ensure reactivity
  const presenceMap = usePresenceStore((state) => {
    const map = state.presenceMap;
    //console.log(`[ContactList] Store selector called, presenceMap keys:`, Object.keys(map));
    // Return the object directly - Zustand will detect changes via shallow comparison
    return map;
  });
  const { data: contacts } = useSuspenseQuery<models.MetaContact[], Error>({
    queryKey: ["metaContacts"],
    queryFn: fetchMetaContacts,
  });
  const typingByConversation = useTypingStore((state) => state.typingByConversation);

  // Track sync status to gray out/hide empty conversations
  const [syncStatus, setSyncStatus] = useState<"syncing" | "completed" | null>(null);
  const [isNewConversationModalOpen, setIsNewConversationModalOpen] = useState(false);
  
  // Track conversations with new reactions (pastille)
  const [conversationsWithNewReactions, setConversationsWithNewReactions] = useState<Set<string>>(new Set());

  // Listen for sync status events
  useEffect(() => {
    const unsubscribe = EventsOn("sync-status", (statusJSON: string) => {
      try {
        const rawStatus: Record<string, any> = JSON.parse(statusJSON);
        const status = (rawStatus.Status || rawStatus.status || null) as string;

        if (status === "completed") {
          setSyncStatus("completed");
          // Invalidate all last messages to update sidebar previews after sync
          queryClient.invalidateQueries({ queryKey: ["lastMessage"] });
        } else if (status === "fetching_contacts" || status === "fetching_history" || status === "fetching_avatars") {
          setSyncStatus("syncing");
        }
      } catch (error) {
        console.error("Failed to parse sync status in ContactList:", error);
      }
    });

    return () => {
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [queryClient]);

  // Listen for contact refresh events
  useEffect(() => {
    const unsubscribe = EventsOn("contacts-refresh", () => {
      // Invalidate and refetch contacts when sync completes or new message arrives
      queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
      queryClient.refetchQueries({ queryKey: ["metaContacts"], type: "active" });
      // Invalidate last message queries to update sorting
      queryClient.invalidateQueries({ queryKey: ["lastMessage"] });
      // Invalidate active calls queries to update call badges
      queryClient.invalidateQueries({ queryKey: ["activeCalls"] });
    });

    return () => {
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [queryClient]);

  // Listen for contact status change events
  useEffect(() => {
    const unsubscribe = EventsOn("contact-status", (statusJSON: string) => {
      try {
        JSON.parse(statusJSON) as {
          UserID: string;
          Status: string;
          StatusEmoji?: string;
          StatusText?: string;
        };
        
        // Invalidate contacts query to refetch with updated status
        // This ensures the UI reflects the latest status and emoji
        queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
        queryClient.refetchQueries({ queryKey: ["metaContacts"], type: "active" });
      } catch (error) {
        console.error("Failed to parse contact-status event:", error, statusJSON);
      }
    });

    return () => {
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [queryClient]);

  // Also listen for new messages to update active call badges
  useEffect(() => {
    const unsubscribe = EventsOn("new-message", () => {
      // Invalidate active calls queries when a new message arrives
      // This ensures the badge disappears immediately when CallTerminate updates the message
      queryClient.invalidateQueries({ queryKey: ["activeCalls"] });
    });

    return () => {
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [queryClient]);

  // Listen for reaction events to show badge on conversations
  useEffect(() => {
    const unsubscribe = EventsOn("reaction", (reactionJSON: string) => {
      try {
        const reaction: {
          ConversationID: string;
          MessageID: string;
          UserID: string;
          Emoji: string;
          Added: boolean;
          Timestamp: number;
        } = JSON.parse(reactionJSON);
        
        // Only show badge for reactions added (not removed)
        // The badge will be cleared when the conversation is opened
        if (reaction.Added && reaction.ConversationID) {
          setConversationsWithNewReactions((prev) => {
            const next = new Set(prev);
            next.add(reaction.ConversationID);
            return next;
          });
        }
      } catch (error) {
        console.error("Failed to parse reaction event in ContactList:", error);
      }
    });

    return () => {
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, []);

  // Clear reaction badge when conversation is selected
  useEffect(() => {
    if (selectedContact?.linkedAccounts?.[0]?.userId) {
      const conversationId = selectedContact.linkedAccounts[0].userId;
      setConversationsWithNewReactions((prev) => {
        const next = new Set(prev);
        next.delete(conversationId);
        return next;
      });
    }
  }, [selectedContact]);

  // Update metaContacts in store
  useEffect(() => {
    setMetaContacts(contacts);
  }, [contacts, setMetaContacts]);

  // Debug: Log when presenceMap changes
  useEffect(() => {
    //console.log(`[ContactList] presenceMap changed, current entries:`, Object.entries(presenceMap));
  }, [presenceMap]);

  // Use shared hook for sorted contacts
  const { sortedContacts: sortedContactsBase, lastMessages } = useSortedContacts(sortBy);

  // Filter contacts by selected provider
  const selectedProviderFilter = useAppStore((state) => state.selectedProviderFilter);
  const sortedContacts = useMemo(() => {
    if (!selectedProviderFilter) {
      return sortedContactsBase;
    }
    const filtered = sortedContactsBase.filter((contact) => {
      const hasMatchingAccount = contact.linkedAccounts.some(
        (account) => account.providerInstanceId === selectedProviderFilter
      );
      if (!hasMatchingAccount) {
        //console.log(`[ContactList] Contact ${contact.displayName} filtered out - no linkedAccount with providerInstanceId=${selectedProviderFilter}. Available:`,
        //contact.linkedAccounts.map(acc => ({ userId: acc.userId, providerInstanceId: acc.providerInstanceId })));
      }
      return hasMatchingAccount;
    });
    //console.log(`[ContactList] Filtered contacts: ${filtered.length} out of ${sortedContactsBase.length} for providerInstanceId=${selectedProviderFilter}`);
    return filtered;
  }, [sortedContactsBase, selectedProviderFilter]);

  const readStateByConversation = useMessageReadStore(
    (state) => state.readByConversation
  );

  const unreadCountsByConversation = useMemo(() => {
    const counts: Record<string, number> = {};
    sortedContacts.forEach((contact) => {
      const conversationId = contact.linkedAccounts[0]?.userId;
      if (!conversationId) {
        return;
      }
      const conversationState = readStateByConversation[conversationId];
      if (!conversationState) {
        counts[conversationId] = 0;
        return;
      }
      const unreadCount = Object.values(conversationState).filter(
        (isRead) => !isRead
      ).length;
      counts[conversationId] = unreadCount;

      // Log pour déboguer les compteurs incorrects
      if (unreadCount > 0) {
        const unreadMessageIds = Object.entries(conversationState)
          .filter(([_, isRead]) => !isRead)
          .map(([msgId, _]) => msgId)
          .slice(0, 5); // Limiter à 5 pour ne pas surcharger les logs
        console.log(`ContactList: Conversation ${conversationId} has ${unreadCount} unread messages. Sample IDs:`, unreadMessageIds);
      }
    });
    return counts;
  }, [readStateByConversation, sortedContacts]);

  // Detect active incoming calls (not terminated) for each conversation
  const activeCallsQueries = useQueries({
    queries: sortedContacts.map((contact) => {
      const conversationId = contact.linkedAccounts[0]?.userId ?? "";
      return {
        queryKey: ["activeCalls", conversationId],
        queryFn: async () => {
          if (!conversationId) return false;
          try {
            const messages = await GetMessagesForConversation(conversationId);

            // Check if there are any active incoming call messages (not terminated)
            // Only show badge for "incoming_call" or "incoming_group_call" types
            const hasActiveCall = (messages || []).some((msg) => {
              if (!msg.callType || msg.callType.trim() === "") {
                return false;
              }
              // Only show badge for incoming calls that haven't been terminated yet
              const callType = msg.callType.trim();
              return callType === "incoming_call" || callType === "incoming_group_call";
            });

            return hasActiveCall;
          } catch (error) {
            console.error(`Error checking active calls for ${conversationId}:`, error);
            return false;
          }
        },
        enabled: !!conversationId,
        staleTime: 5000, // Cache for 5 seconds (more frequent updates for active calls)
      };
    }),
  });

  const hasActiveCallByConversation = useMemo(() => {
    const calls: Record<string, boolean> = {};
    sortedContacts.forEach((contact, index) => {
      const conversationId = contact.linkedAccounts[0]?.userId ?? "";
      if (conversationId && activeCallsQueries[index]?.data) {
        calls[conversationId] = activeCallsQueries[index].data;
      }
    });
    return calls;
  }, [sortedContacts, activeCallsQueries]);

  // Get message counts for each conversation to determine if empty
  const messageCountQueries = useQueries({
    queries: sortedContacts.map((contact) => {
      const conversationId = contact.linkedAccounts[0]?.userId ?? "";
      return {
        queryKey: ["messageCount", conversationId],
        queryFn: async () => {
          if (!conversationId) return 0;
          try {
            const messages = await GetMessagesForConversation(conversationId);
            return messages?.length ?? 0;
          } catch (error) {
            console.error(`Error getting message count for ${conversationId}:`, error);
            return 0;
          }
        },
        enabled: !!conversationId,
        staleTime: 30000, // Cache for 30 seconds
      };
    }),
  });

  const messageCountByConversation = useMemo(() => {
    const counts: Record<string, number> = {};
    sortedContacts.forEach((contact, index) => {
      const conversationId = contact.linkedAccounts[0]?.userId ?? "";
      if (conversationId) {
        counts[conversationId] = messageCountQueries[index]?.data ?? 0;
      }
    });
    return counts;
  }, [sortedContacts, messageCountQueries]);

  // Don't filter contacts based on message count - just show all
  // We only gray them out during sync, but keep them visible
  const filteredContacts = sortedContacts;

  if (contacts.length === 0) {
    return (
      <div className="flex flex-col h-full">
        <div className="p-2 border-b">
          <h2 className="text-lg font-semibold">{t("contacts")}</h2>
        </div>
        <div className="flex-1"></div>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      <div className="p-2 border-b space-y-2">
        <h2 className="text-base font-semibold">{t("contacts")}</h2>
        <div className="flex gap-1">
          <Button
            variant={sortBy === "alphabetical" ? "default" : "ghost"}
            size="sm"
            className="flex-1 text-xs"
            onClick={() => setSortBy("alphabetical")}
          >
            <ArrowDownAZ className="h-3 w-3 mr-1" />
            A-Z
          </Button>
          <Button
            variant={sortBy === "last_message" ? "default" : "ghost"}
            size="sm"
            className="flex-1 text-xs"
            onClick={() => setSortBy("last_message")}
          >
            <Clock className="h-3 w-3 mr-1" />
            Recent
          </Button>
        </div>
      </div>
      <div className="flex-1 overflow-y-auto scroll-area">
        <div className="space-y-1 p-2">
          {filteredContacts.map((contact) => {
            const conversationId = contact.linkedAccounts[0]?.userId ?? "";
            const unreadCount = unreadCountsByConversation[conversationId] ?? 0;
            const displayUnreadCount =
              unreadCount > 99 ? "99+" : unreadCount.toString();
            const isSelected = selectedContact?.id === contact.id;
            const isTyping = (typingByConversation[conversationId]?.length ?? 0) > 0;
            const messageCount = messageCountByConversation[conversationId] ?? 0;
            const isEmptyDuringSync = syncStatus === "syncing" && messageCount === 0;

            // Check if contact is online (only for DM, not groups)
            const isGroup = conversationId.endsWith("@g.us");

            // Helper to check if a LID in presenceMap matches any linkedAccount
            const checkPresenceMatch = () => {
              if (isGroup) {
                //console.log(`[ContactList] Skipping presence check for group: ${contact.displayName}`);
                return false;
              }

              // First, check if any linkedAccount has status "online" (for Slack and other providers)
              const statusMatch = contact.linkedAccounts.some(
                (account) => account.status === "online"
              );
              if (statusMatch) {
                return true;
              }

              //console.log(`[ContactList] Checking presence for ${contact.displayName}, linkedAccounts:`, contact.linkedAccounts.map(a => a.userId));
              //console.log(`[ContactList] Current presenceMap:`, presenceMap);

              // First, try direct match
              const directMatch = contact.linkedAccounts.some(
                (account) => {
                  const isOnline = presenceMap[account.userId] === true;
                  //console.log(`[ContactList] Checking direct match for ${account.userId}: ${isOnline}`);
                  if (isOnline) {
                    //console.log(`[ContactList] ✓ Direct match found for ${contact.displayName}: ${account.userId}`);
                  }
                  return isOnline;
                }
              );
              if (directMatch) return true;

              // If no direct match, try to match by phone number
              // LID format: "149044005437527@lid" or "216350555386047@lid"
              // JID format: "33XXXXXXXXX@s.whatsapp.net"
              //console.log(`[ContactList] No direct match, trying phone number matching...`);
              for (const [lid, isOnline] of Object.entries(presenceMap)) {
                if (!isOnline || !lid.endsWith("@lid")) continue;

                // Extract phone number from LID (remove @lid and any :X suffix)
                const lidPhone = lid.replace(/@lid$/, "").replace(/:\d+$/, "");
                //console.log(`[ContactList] Checking LID ${lid} (extracted phone: ${lidPhone})`);

                // Check if any linkedAccount contains this phone number
                const phoneMatch = contact.linkedAccounts.some(account => {
                  const jid = account.userId;
                  // Extract phone from JID (e.g., "33677815440@s.whatsapp.net" -> "33677815440")
                  const jidPhone = jid.split("@")[0];
                  const matches = jidPhone === lidPhone;
                  //console.log(`[ContactList]   Comparing LID phone ${lidPhone} with JID ${jid} (phone: ${jidPhone}): ${matches}`);
                  if (matches) {
                    //console.log(`[ContactList] ✓ Phone match: LID ${lid} (phone: ${lidPhone}) matches JID ${jid} (phone: ${jidPhone}) for contact ${contact.displayName}`);
                  }
                  return matches;
                });

                if (phoneMatch) {
                  return true;
                }
              }

              //console.log(`[ContactList] ✗ No match found for ${contact.displayName}`);
              return false;
            };

            const isOnline = checkPresenceMatch();
            //console.log(`[ContactList] Final isOnline status for ${contact.displayName}: ${isOnline}`);

            // Check if this contact represents the current user
            // In DMs, we typically don't see ourselves, but check anyway
            // We'll identify current user by checking if any linkedAccount userId appears in presence
            // as being from "me" - but since presence doesn't track "me", we'll skip this check for now
            // and rely on the fact that DMs don't show self-contacts
            const isCurrentUser = false; // TODO: Implement proper current user detection if needed

            return (
              <div
                key={contact.id}
                className={cn(
                  "flex items-center space-x-3 p-2 rounded-lg cursor-pointer transition-colors border border-transparent",
                  "hover:bg-muted",
                  isSelected && "ring-1 ring-primary/40",
                  isEmptyDuringSync && "opacity-50",
                  unreadCount > 0 && !isSelected && "bg-muted/50 font-medium"
                )}
                onClick={() => setSelectedContact(contact)}
              >
                <div className="relative">
                  <Avatar>
                    <AvatarImage src={contact.avatarUrl} alt={contact.displayName} />
                    <AvatarFallback>
                      {contact.displayName.substring(0, 2).toUpperCase()}
                    </AvatarFallback>
                  </Avatar>
                  {/* Status emoji overlay */}
                  {!isCurrentUser && (() => {
                    const statusEmojiData = getContactStatusEmoji(contact);
                    if (statusEmojiData) {
                      return (
                        <div
                          className="absolute -top-1 -left-1 bg-background rounded-full p-0.5 border border-border shadow-sm flex items-center justify-center"
                          title={statusEmojiData.emoji}
                        >
                          <SlackEmoji
                            emoji={statusEmojiData.emoji}
                            providerInstanceId={statusEmojiData.providerInstanceId}
                            size={12}
                          />
                        </div>
                      );
                    }
                    return null;
                  })()}
                  {!isCurrentUser && !isTyping && (() => {
                    // Get status from linked accounts (prefer first account with a status)
                    const accountStatus = contact.linkedAccounts.find(acc => acc.status && acc.status !== "offline")?.status || null;
                    const status = accountStatus || (isOnline ? "online" : null);
                    
                    if (!status) return null;
                    
                    // Special handling for meeting status - show calendar icon
                    if (status === "meeting") {
                      return (
                    <div
                          className="absolute -bottom-0.5 -right-0.5 h-3.5 w-3.5 rounded bg-blue-500 border-2 border-background flex items-center justify-center"
                          title={t("meeting") || "En réunion"}
                        >
                          <Calendar className="h-2 w-2 text-white" />
                        </div>
                      );
                    }
                    
                    // Determine status badge color and title for other statuses
                    let bgColor = "";
                    let titleText = "";
                    
                    switch (status) {
                      case "online":
                        bgColor = "bg-green-500";
                        titleText = t("online");
                        break;
                      case "away":
                        bgColor = "bg-yellow-500";
                        titleText = t("away") || "Away";
                        break;
                      case "busy":
                        bgColor = "bg-red-500";
                        titleText = t("busy") || "Busy";
                        break;
                      case "holiday":
                        bgColor = "bg-purple-500";
                        titleText = t("holiday") || "Holiday";
                        break;
                      default:
                        bgColor = "bg-gray-500";
                        titleText = status;
                    }
                    
                    return (
                      <div
                        className={`absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full ${bgColor} border-2 border-background`}
                        title={titleText}
                    />
                    );
                  })()}
                  {isTyping && (
                    <div
                      className="absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full bg-green-500 border-2 border-background animate-pulse"
                      title={t("typing_indicator_title")}
                    />
                  )}
                </div>
                <div className="flex flex-col flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                  <span className="text-sm font-medium truncate">
                    {contact.displayName}
                  </span>
                  <div className="ml-auto flex items-center gap-1.5">
                    {hasActiveCallByConversation[conversationId] && (
                      <div
                        className="inline-flex items-center justify-center rounded-full bg-orange-600 dark:bg-orange-500 p-1.5"
                        title={t("call.activeCall")}
                        aria-label={t("call.activeCall")}
                      >
                        <Phone className="h-3 w-3 text-white" />
                      </div>
                    )}
                    {conversationsWithNewReactions.has(conversationId) && (
                      <div
                        className="inline-flex items-center justify-center rounded-full bg-purple-600 dark:bg-purple-500 p-1.5"
                        title={t("new_reaction_badge")}
                        aria-label={t("new_reaction_badge")}
                      >
                        <Smile className="h-3 w-3 text-white" />
                      </div>
                    )}
                    {unreadCount > 0 && (
                      <span
                        className="inline-flex min-w-[1.75rem] justify-center rounded-full bg-blue-600 dark:bg-blue-500 px-2 py-0.5 text-[11px] font-semibold text-white"
                        aria-label={t("unread_badge_aria", {
                          count: unreadCount,
                        })}
                      >
                        {displayUnreadCount}
                      </span>
                    )}
                  </div>
                  </div>
                  {/* Last message preview */}
                  {(() => {
                    const lastMessage = lastMessages[conversationId];
                    if (lastMessage?.body) {
                      return (
                        <div className="text-xs text-muted-foreground mt-0.5 text-left overflow-hidden whitespace-nowrap text-ellipsis">
                          <MessageText
                            text={lastMessage.body}
                            providerInstanceId={contact.linkedAccounts[0]?.providerInstanceId}
                            isSlack={contact.linkedAccounts[0]?.protocol === "slack"}
                            emojiSize={12}
                            className="inline"
                            preview={true}
                          />
                        </div>
                      );
                    }
                    return null;
                  })()}
                </div>
              </div>
            );
          })}
        </div>
      </div>

      {/* Floating Action Button for New Conversation */}
      <div className="absolute bottom-6 right-6 z-10">
        <Button
          size="icon"
          className="h-12 w-12 rounded-full shadow-lg hover:shadow-xl transition-shadow"
          onClick={() => setIsNewConversationModalOpen(true)}
          title={t("new_conversation")}
        >
          <Plus className="h-6 w-6" />
        </Button>
      </div>

      <NewConversationModal
        open={isNewConversationModalOpen}
        onOpenChange={setIsNewConversationModalOpen}
      />
    </div>
  );
}
