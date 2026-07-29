import { ArrowDownAZ, Calendar, Clock, Inbox, MessageSquarePlus, Phone, Search } from "lucide-react";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { GetAllActiveCalls, GetAllMessageCounts, GetCapabilities, GetConfiguredProviders, GetMetaContacts } from "../../wailsjs/go/main/App";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Emoji } from "./Emoji";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { MessageText } from "./MessageText";
import { NewConversationModal } from "./NewConversationModal";
import { cn } from "@/lib/utils";
import { getContactStatusEmoji } from "@/lib/statusEmoji";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { usePresenceStore } from "@/lib/presenceStore";
import { useSortedContacts } from "@/hooks/useSortedContacts";
import { useTranslation } from "react-i18next";
import { useTypingStore } from "@/lib/typingStore";
import { TypingIndicator } from "./TypingIndicator";

// Wrapper function to use Wails with React Query's suspense mode
const fetchMetaContacts = async () => {
  return GetMetaContacts();
};

export function ContactList({ onOpenSearch }: { onOpenSearch: () => void }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const selectedContact = useAppStore((state) => state.selectedContact);
  const setSelectedContact = useAppStore((state) => state.setSelectedContact);
  const setMetaContacts = useAppStore((state) => state.setMetaContacts);
  const setCapabilities = useAppStore((state) => state.setCapabilities);
  const sortBy = useAppStore((state) => state.contactSortBy);
  const setSortBy = useAppStore((state) => state.setContactSortBy);
  const [hasInitializedSort, setHasInitializedSort] = useState(false);

  // Check if this is the first provider configuration (no messages yet)
  // If so, default to alphabetical sorting
  const { data: configuredProviders = [] } = useQuery({
    queryKey: ["configuredProviders"],
    queryFn: async () => {
      try {
        return await GetConfiguredProviders();
      } catch (error) {
        console.error("Failed to fetch configured providers:", error);
        return [];
      }
    },
  });

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
    // Always refetch on mount to catch status updates that fired before this
    // component's useEffect listeners were installed.
    refetchOnMount: "always",
    staleTime: 0,
  });
  const typingByConversation = useTypingStore((state) => state.typingByConversation);

  // Track sync status to gray out/hide empty conversations
  const [syncStatus, setSyncStatus] = useState<"syncing" | "completed" | null>(null);
  const [isNewConversationModalOpen, setIsNewConversationModalOpen] = useState(false);

  // Listen for sync status events
  useEffect(() => {
    const unsubscribe = EventsOn("sync-status", (statusJSON: string) => {
      try {
        const rawStatus: Record<string, any> = JSON.parse(statusJSON);
        const status = (rawStatus.Status || rawStatus.status || null) as string;

        if (status === "completed") {
          setSyncStatus("completed");
          // Trigger a full refresh when sync completes to ensure perfect sorting and counts
          queryClient.invalidateQueries({ queryKey: ["allLastMessages"] });
          queryClient.invalidateQueries({ queryKey: ["allLastMessageTimestamps"] });
          queryClient.invalidateQueries({ queryKey: ["allMessageCounts"] });
          queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
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
  }, []);

  // Listen for contact refresh events
  // Use a ref to debounce to avoid high-frequency refetches during mass sync
  const refreshTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    const unsubscribe = EventsOn("contacts-refresh", () => {
      if (refreshTimeoutRef.current) {
        clearTimeout(refreshTimeoutRef.current);
      }

      refreshTimeoutRef.current = setTimeout(() => {
        // Invalidate and refetch contacts when sync completes or new message arrives
        queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
        queryClient.refetchQueries({ queryKey: ["metaContacts"], type: "active" });
        // Invalidate last message queries to update sorting and previews
        queryClient.invalidateQueries({ queryKey: ["lastMessage"] });
        queryClient.invalidateQueries({ queryKey: ["allLastMessages"] });
        queryClient.invalidateQueries({ queryKey: ["allLastMessageTimestamps"] });
        // Invalidate active calls queries to update call badges
        queryClient.invalidateQueries({ queryKey: ["activeCalls"] });
      }, 500); // 500ms debounce
    });

    return () => {
      if (refreshTimeoutRef.current) {
        clearTimeout(refreshTimeoutRef.current);
      }
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [queryClient, refreshTimeoutRef]);

  // Listen for contact status change events
  useEffect(() => {
    const unsubscribe = EventsOn("contact-status", (statusJSON: string) => {
      try {
        JSON.parse(statusJSON) as {
          userId: string;
          status: string;
          statusEmoji?: string;
          statusText?: string;
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

  // Also listen for new messages to update active call badges and previews/sorting
  useEffect(() => {
    const unsubscribe = EventsOn("new-message", () => {
      if (refreshTimeoutRef.current) {
        clearTimeout(refreshTimeoutRef.current);
      }

      refreshTimeoutRef.current = setTimeout(() => {
        // Invalidate active calls queries when a new message arrives
        // This ensures the badge disappears immediately when CallTerminate updates the message
        queryClient.invalidateQueries({ queryKey: ["activeCalls"] });

        // Invalidate last message queries to update sorting and previews
        queryClient.invalidateQueries({ queryKey: ["allLastMessages"] });
        queryClient.invalidateQueries({ queryKey: ["allLastMessageTimestamps"] });
        queryClient.invalidateQueries({ queryKey: ["allMessageCounts"] });

        // Optionally refetch contacts if names/avatars might have changed (though usually just messages)
        // queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
      }, 300); // 300ms debounce
    });

    return () => {
      if (refreshTimeoutRef.current) {
        clearTimeout(refreshTimeoutRef.current);
      }
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [queryClient, refreshTimeoutRef]);

  // Update metaContacts in store
  useEffect(() => {
    setMetaContacts(contacts);
  }, [contacts, setMetaContacts]);

  // Temporary badge diagnostic — remove after debugging
  useEffect(() => {
    const onlinePresenceKeys = Object.entries(presenceMap).filter(([, v]) => v).map(([k]) => k);
    // Find contacts whose userId matches a presenceMap key
    const presenceMatches = contacts.flatMap(c =>
      c.linkedAccounts
        .filter(a => presenceMap[a.userId] === true)
        .map(a => `${c.displayName} [userId=${a.userId} isGroup=${a.isGroup}]`)
    );
    const nonOffline = contacts.flatMap(c =>
      c.linkedAccounts
        .filter(a => a.status && a.status !== "offline")
        .map(a => `${c.displayName} [${a.protocol}:${a.status}]`)
    );
    console.log(`[badge-diag] contacts=${contacts.length} presenceKeys=${onlinePresenceKeys.join(",") || "(none)"}`);
    console.log(`[badge-diag] presenceMatches:`, presenceMatches.length > 0 ? presenceMatches : "(none)");
    console.log(`[badge-diag] non-offline-statuses:`, nonOffline.length > 0 ? nonOffline : "(none)");
  }, [contacts, presenceMap]);

  // Use shared hook for sorted contacts
  const { sortedContacts: sortedContactsBase, lastMessages } = useSortedContacts(sortBy);

  // Safety net: ensure all last messages are registered in the message read store
  const registerIncomingMessage = useMessageReadStore((state) => state.registerIncomingMessage);
  useEffect(() => {
    Object.values(lastMessages).forEach((msg) => {
      if (msg) {
        registerIncomingMessage(msg);
      }
    });
  }, [lastMessages, registerIncomingMessage]);

  // Filter contacts by selected provider
  const selectedProviderFilter = useAppStore((state) => state.selectedProviderFilter);
  const sortedContacts = useMemo(() => {
    if (!selectedProviderFilter) {
      return sortedContactsBase;
    }
    const filtered = sortedContactsBase.filter((contact) => {
      const hasMatchingAccount = (contact.linkedAccounts ?? []).some(
        (account) => account.providerInstanceId === selectedProviderFilter
      );
      return hasMatchingAccount;
    });
    console.log(`[FILTER DEBUG] filter=${selectedProviderFilter} → ${filtered.length}/${sortedContactsBase.length} contacts`);
    if (filtered.length > 0) {
      const sample = filtered.slice(0, 3).map(c => ({
        name: c.displayName,
        accounts: c.linkedAccounts.map(a => `${a.providerInstanceId}/${a.userId?.slice(0, 12)}`),
      }));
      console.log("[FILTER DEBUG] sample:", sample);
    }
    return filtered;
  }, [sortedContactsBase, selectedProviderFilter]);

  const readStateByConversation = useMessageReadStore(
    (state) => state.readByConversation
  );

  const unreadCountsByConversation = useMemo(() => {
    const counts: Record<string, number> = {};
    sortedContacts.forEach((contact) => {
      const conversationId =
        contact.linkedAccounts[0]?.conversationId ??
        contact.linkedAccounts[0]?.userId;
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

  const totalUnreadCount = useMemo(
    () => Object.values(unreadCountsByConversation).reduce((sum, n) => sum + n, 0),
    [unreadCountsByConversation]
  );

  // Detect active incoming calls (not terminated) for all conversations in one query
  // This is much more efficient than making individual queries for each conversation
  const { data: allActiveCalls = {} } = useQuery<Record<string, boolean>, Error>({
    queryKey: ["allActiveCalls"],
        queryFn: async () => {
          try {
        const activeCalls = await GetAllActiveCalls();
        return activeCalls || {};
          } catch (error) {
        console.error("Error fetching all active calls:", error);
        return {};
          }
        },
        staleTime: 5000, // Cache for 5 seconds (more frequent updates for active calls)
        placeholderData: (previousData) => previousData,
  });

  const hasActiveCallByConversation = useMemo(() => {
    return allActiveCalls;
  }, [allActiveCalls]);

  // Get message counts for all conversations in a single query
  // This is much more efficient than making individual queries for each conversation
  const { data: allMessageCounts = {}, isPending: allMessageCountsIsPending } = useQuery<Record<string, number>, Error>({
    queryKey: ["allMessageCounts"],
        queryFn: async () => {
          try {
        const counts = await GetAllMessageCounts();
        return counts || {};
          } catch (error) {
        console.error("Error fetching all message counts:", error);
        return {};
          }
        },
        staleTime: 30000, // Cache for 30 seconds
        placeholderData: (previousData) => previousData,
  });

  const messageCountByConversation = useMemo(() => {
    const counts: Record<string, number> = {};
    sortedContacts.forEach((contact) => {
      const conversationId =
        contact.linkedAccounts[0]?.conversationId ??
        contact.linkedAccounts[0]?.userId ??
        "";
      if (conversationId) {
        counts[conversationId] = allMessageCounts[conversationId] ?? 0;
      }
    });
    return counts;
  }, [sortedContacts, allMessageCounts]);

  // Initialize sort order based on whether there are messages
  // If no messages and providers are configured, default to alphabetical
  // IMPORTANT: wait for allMessageCounts to finish loading before deciding —
  // an empty {} during loading would incorrectly switch the tab to alphabetical,
  // disabling the allLastMessageTimestamps query and emptying the "recent" tab.
  useEffect(() => {
    if (configuredProviders.length === 0) return;
    configuredProviders.forEach((provider) => {
      const id = provider.instanceId;
      if (!id) return;
      GetCapabilities(id)
        .then((caps) => setCapabilities(id, caps))
        .catch(() => {});
    });
  }, [configuredProviders, setCapabilities]);

  useEffect(() => {
    if (hasInitializedSort || contacts.length === 0 || allMessageCountsIsPending) {
      return;
    }

    // Check if any contact has messages
    const hasMessages = Object.values(allMessageCounts).some((count) => count > 0);

    // If no messages and providers are configured, default to alphabetical
    if (!hasMessages && configuredProviders.length > 0 && sortBy === "last_message") {
      setSortBy("alphabetical");
    }

    setHasInitializedSort(true);
  }, [contacts.length, allMessageCounts, allMessageCountsIsPending, configuredProviders.length, hasInitializedSort, sortBy]);

  // Filter contacts based on sort option
  // For "unread", only show conversations with unread messages
  const filteredContacts = useMemo(() => {
    if (sortBy === "unread") {
      return sortedContacts.filter((contact) => {
        const conversationId =
          contact.linkedAccounts[0]?.conversationId ??
          contact.linkedAccounts[0]?.userId ??
          "";
        const unreadCount = unreadCountsByConversation[conversationId] ?? 0;
        return unreadCount > 0;
      });
    }
    // For other sort options, show all
    return sortedContacts;
  }, [sortedContacts, sortBy, unreadCountsByConversation]);

  return (
    <div className="contact-list flex flex-col h-full bg-sidebar text-sidebar-foreground">
      <div className="contact-list__header px-3 pt-3 pb-2 border-b border-sidebar-border space-y-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-sidebar-foreground">{t("conversations")}</h2>
          <div className="contact-list__header-actions flex items-center gap-1">
            <Button
              variant="ghost"
              size="icon"
              className="contact-list__search-button h-7 w-7 rounded-md shrink-0 hover:bg-sidebar-hover text-sidebar-muted-foreground hover:text-sidebar-foreground"
              onClick={onOpenSearch}
              title={t("search_placeholder")}
            >
              <Search className="h-3.5 w-3.5" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="contact-list__new-conversation-button h-7 w-7 rounded-md shrink-0 hover:bg-sidebar-hover text-sidebar-muted-foreground hover:text-sidebar-foreground"
              onClick={() => setIsNewConversationModalOpen(true)}
              title={t("new_conversation")}
            >
              <MessageSquarePlus className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
        {/* Sort tabs — icon only */}
        <div className="contact-list__sort-tabs flex gap-1">
          <button
            className={cn(
              "contact-list__sort-tab flex-1 h-7 flex items-center justify-center rounded-md text-xs transition-colors",
              sortBy === "alphabetical"
                ? "bg-sidebar-active text-sidebar-active-foreground"
                : "text-sidebar-muted-foreground hover:bg-sidebar-hover hover:text-sidebar-foreground"
            )}
            onClick={() => setSortBy("alphabetical")}
            title={t("alphabetical") || "Alphabetical"}
          >
            <ArrowDownAZ className="h-3.5 w-3.5" />
          </button>
          <button
            className={cn(
              "contact-list__sort-tab flex-1 h-7 flex items-center justify-center rounded-md text-xs transition-colors",
              sortBy === "last_message"
                ? "bg-sidebar-active text-sidebar-active-foreground"
                : "text-sidebar-muted-foreground hover:bg-sidebar-hover hover:text-sidebar-foreground"
            )}
            onClick={() => setSortBy("last_message")}
            title={t("recent") || "Recent"}
          >
            <Clock className="h-3.5 w-3.5" />
          </button>
          <button
            className={cn(
              "contact-list__sort-tab flex-1 h-7 flex items-center justify-center rounded-md text-xs transition-colors relative",
              sortBy === "unread"
                ? "bg-sidebar-active text-sidebar-active-foreground"
                : "text-sidebar-muted-foreground hover:bg-sidebar-hover hover:text-sidebar-foreground"
            )}
            onClick={() => setSortBy("unread")}
            title={t("unread") || "Unread"}
          >
            <Inbox className="h-3.5 w-3.5" />
            {totalUnreadCount > 0 && (
              <span className="absolute -top-1 -right-1 h-3.5 min-w-3.5 px-0.5 rounded-full bg-blue-600 dark:bg-blue-500 text-white text-[9px] font-bold leading-3.5 text-center">
                {totalUnreadCount > 99 ? "99+" : totalUnreadCount}
              </span>
            )}
          </button>
        </div>
      </div>
      <div className="contact-list__scroll flex-1 overflow-y-auto scroll-area">
        <div className="space-y-0.5 py-2 px-1">
          {sortBy === "unread" && filteredContacts.length === 0 && (
            <div className="flex flex-col items-center justify-center h-full py-12 px-4 text-center">
              <Inbox className="h-12 w-12 text-sidebar-muted-foreground/30 mb-4" />
              <p className="text-sm text-sidebar-muted-foreground mb-4">
                {t("no_unread_messages") || "Pas de messages non lus"}
              </p>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setSortBy("last_message")}
              >
                {t("view_recent") || "Voir les messages récents"}
              </Button>
            </div>
          )}
          {filteredContacts.map((contact) => {
            const accountConversationId = (account: models.LinkedAccount) =>
              account.conversationId ?? account.userId ?? "";
            const typingAccount = contact.linkedAccounts.find(
              (account) => (typingByConversation[accountConversationId(account)]?.length ?? 0) > 0
            );
            const conversationId = accountConversationId(contact.linkedAccounts[0]);
            const typingConversationId = typingAccount ? accountConversationId(typingAccount) : "";
            const unreadCount = unreadCountsByConversation[conversationId] ?? 0;
            const displayUnreadCount =
              unreadCount > 99 ? "99+" : unreadCount.toString();
            const isSelected = selectedContact?.id === contact.id;
            const isTyping = typingConversationId !== "";
            const messageCount = messageCountByConversation[conversationId] ?? 0;
            const isEmptyDuringSync = syncStatus === "syncing" && messageCount === 0;

            const isGroup = contact.linkedAccounts[0]?.isGroup;

            const checkPresenceMatch = () => {
              if (isGroup) return false;
              const statusMatch = contact.linkedAccounts.some(
                (account) => account.status === "online"
              );
              if (statusMatch) return true;
              return contact.linkedAccounts.some(
                (account) => presenceMap[account.userId] === true
              );
            };

            const isOnline = checkPresenceMatch();
            const isCurrentUser = false;

            return (
              <div
                key={contact.id}
                className={cn(
                  "contact-list__item flex items-center space-x-3 px-2 py-2 rounded-lg cursor-pointer transition-colors",
                  isSelected
                    ? "bg-sidebar-active text-sidebar-active-foreground"
                    : "text-sidebar-foreground hover:bg-sidebar-hover",
                  isEmptyDuringSync && "opacity-50",
                  !isSelected && unreadCount > 0 && "bg-sidebar-hover/60"
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
                          className="absolute -top-1 -left-1 bg-background rounded-full p-0.5 border border-border shadow-sm flex items-center justify-center "
                          title={statusEmojiData.emoji}
                        >
                          <Emoji
                            emoji={statusEmojiData.emoji}
                            providerInstanceId={statusEmojiData.providerInstanceId}
                            size={12}
                          />
                        </div>
                      );
                    }
                    return null;
                  })()}
                  {!isCurrentUser && !isTyping && !isGroup && (() => {
                    // Get status from linked accounts (prefer first account with a status)
                    const accountStatus = contact.linkedAccounts.find(acc => acc.status && acc.status !== "offline")?.status || null;
                    const status = accountStatus || (isOnline ? "online" : null);

                    if (!status) return null;

                    // Special handling for meeting status - show calendar icon
                    if (status === "meeting") {
                      return (
                    <div
                          className="absolute -bottom-0.5 -right-0.5 h-3.5 w-3.5 rounded bg-blue-500 border-2 border-sidebar flex items-center justify-center"
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
                        className={`absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full ${bgColor} border-2 border-sidebar`}
                        title={titleText}
                    />
                    );
                  })()}
                </div>
                <div className="contact-list__item-content flex flex-col flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className={cn(
                      "contact-list__item-name text-sm truncate",
                      unreadCount > 0 ? "font-semibold" : "font-medium"
                    )}>
                      {contact.displayName}
                    </span>
                    <div className="ml-auto flex items-center gap-1.5 shrink-0">
                      {hasActiveCallByConversation[conversationId] && (
                        <div
                          className="inline-flex items-center justify-center rounded-full bg-orange-600 dark:bg-orange-500 p-1.5"
                          title={t("call.activeCall")}
                          aria-label={t("call.activeCall")}
                        >
                          <Phone className="h-3 w-3 text-white" />
                        </div>
                      )}
                      {unreadCount > 0 && (
                        <span
                          className="contact-list__unread-badge inline-flex min-w-[1.5rem] justify-center rounded-full bg-blue-600 dark:bg-blue-500 px-1.5 py-0.5 text-[10px] font-semibold text-white"
                          aria-label={t("unread_badge_aria", { count: unreadCount })}
                        >
                          {displayUnreadCount}
                        </span>
                      )}
                    </div>
                  </div>
                  {/* Last message preview */}
                  {(() => {
                    if (isTyping) {
                      return <TypingIndicator conversationId={typingConversationId} variant="list" />;
                    }
                    const lastMessage = lastMessages[conversationId];
                    if (lastMessage?.body) {
                      return (
                        <div className={cn(
                          "contact-list__item-preview text-xs mt-0.5 text-left overflow-hidden whitespace-nowrap text-ellipsis",
                          isSelected ? "opacity-75" : "text-sidebar-muted-foreground"
                        )}>
                          <MessageText
                            text={lastMessage.body}
                            providerInstanceId={contact.linkedAccounts[0]?.providerInstanceId}
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


      <NewConversationModal
        open={isNewConversationModalOpen}
        onOpenChange={setIsNewConversationModalOpen}
      />
    </div>
  );
}
