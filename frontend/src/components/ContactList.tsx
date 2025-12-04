import { ArrowDownAZ, Clock } from "lucide-react";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { useEffect, useMemo, useState } from "react";
import { useQueryClient, useSuspenseQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { GetMetaContacts } from "../../wailsjs/go/main/App";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useTypingStore } from "@/lib/typingStore";
import { usePresenceStore } from "@/lib/presenceStore";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useSortedContacts } from "@/hooks/useSortedContacts";

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
    console.log(`[ContactList] Store selector called, presenceMap keys:`, Object.keys(map));
    // Return the object directly - Zustand will detect changes via shallow comparison
    return map;
  });
  const { data: contacts } = useSuspenseQuery<models.MetaContact[], Error>({
    queryKey: ["metaContacts"],
    queryFn: fetchMetaContacts,
  });
  const typingByConversation = useTypingStore((state) => state.typingByConversation);

  // Listen for contact refresh events
  useEffect(() => {
    const unsubscribe = EventsOn("contacts-refresh", () => {
      // Invalidate and refetch contacts when sync completes or new message arrives
      queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
      queryClient.refetchQueries({ queryKey: ["metaContacts"], type: "active" });
      // Invalidate last message queries to update sorting
      queryClient.invalidateQueries({ queryKey: ["lastMessage"] });
    });

    return () => {
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [queryClient]);

  // Update metaContacts in store
  useEffect(() => {
    setMetaContacts(contacts);
  }, [contacts, setMetaContacts]);

  // Debug: Log when presenceMap changes
  useEffect(() => {
    console.log(`[ContactList] presenceMap changed, current entries:`, Object.entries(presenceMap));
  }, [presenceMap]);

  // Use shared hook for sorted contacts
  const sortedContacts = useSortedContacts(sortBy);

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
          {sortedContacts.map((contact) => {
            const conversationId = contact.linkedAccounts[0]?.userId ?? "";
            const unreadCount = unreadCountsByConversation[conversationId] ?? 0;
            const displayUnreadCount =
              unreadCount > 99 ? "99+" : unreadCount.toString();
            const isSelected = selectedContact?.id === contact.id;
            const isTyping = (typingByConversation[conversationId]?.length ?? 0) > 0;

            // Check if contact is online (only for DM, not groups)
            const isGroup = conversationId.endsWith("@g.us");

            // Helper to check if a LID in presenceMap matches any linkedAccount
            const checkPresenceMatch = () => {
              if (isGroup) {
                console.log(`[ContactList] Skipping presence check for group: ${contact.displayName}`);
                return false;
              }

              console.log(`[ContactList] Checking presence for ${contact.displayName}, linkedAccounts:`, contact.linkedAccounts.map(a => a.userId));
              console.log(`[ContactList] Current presenceMap:`, presenceMap);

              // First, try direct match
              const directMatch = contact.linkedAccounts.some(
                (account) => {
                  const isOnline = presenceMap[account.userId] === true;
                  console.log(`[ContactList] Checking direct match for ${account.userId}: ${isOnline}`);
                  if (isOnline) {
                    console.log(`[ContactList] ✓ Direct match found for ${contact.displayName}: ${account.userId}`);
                  }
                  return isOnline;
                }
              );
              if (directMatch) return true;

              // If no direct match, try to match by phone number
              // LID format: "149044005437527@lid" or "216350555386047@lid"
              // JID format: "33XXXXXXXXX@s.whatsapp.net"
              console.log(`[ContactList] No direct match, trying phone number matching...`);
              for (const [lid, isOnline] of Object.entries(presenceMap)) {
                if (!isOnline || !lid.endsWith("@lid")) continue;

                // Extract phone number from LID (remove @lid and any :X suffix)
                const lidPhone = lid.replace(/@lid$/, "").replace(/:\d+$/, "");
                console.log(`[ContactList] Checking LID ${lid} (extracted phone: ${lidPhone})`);

                // Check if any linkedAccount contains this phone number
                const phoneMatch = contact.linkedAccounts.some(account => {
                  const jid = account.userId;
                  // Extract phone from JID (e.g., "33677815440@s.whatsapp.net" -> "33677815440")
                  const jidPhone = jid.split("@")[0];
                  const matches = jidPhone === lidPhone;
                  console.log(`[ContactList]   Comparing LID phone ${lidPhone} with JID ${jid} (phone: ${jidPhone}): ${matches}`);
                  if (matches) {
                    console.log(`[ContactList] ✓ Phone match: LID ${lid} (phone: ${lidPhone}) matches JID ${jid} (phone: ${jidPhone}) for contact ${contact.displayName}`);
                  }
                  return matches;
                });

                if (phoneMatch) {
                  return true;
                }
              }

              console.log(`[ContactList] ✗ No match found for ${contact.displayName}`);
              return false;
            };

            const isOnline = checkPresenceMatch();
            console.log(`[ContactList] Final isOnline status for ${contact.displayName}: ${isOnline}`);

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
                  isSelected && "ring-1 ring-primary/40"
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
                  {!isCurrentUser && isOnline && (
                    <div
                      className="absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full bg-green-500 border-2 border-background"
                      title={t("online")}
                    />
                  )}
                  {isTyping && (
                    <div
                      className="absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full bg-green-500 border-2 border-background animate-pulse"
                      title={t("typing_indicator_title")}
                    />
                  )}
                </div>
                <div className="flex items-center gap-2 flex-1 min-w-0">
                  <span className="text-sm font-medium truncate">
                    {contact.displayName}
                  </span>
                  {unreadCount > 0 && (
                    <span
                      className="ml-auto inline-flex min-w-[1.75rem] justify-center rounded-full bg-blue-600 dark:bg-blue-500 px-2 py-0.5 text-[11px] font-semibold text-white"
                      aria-label={t("unread_badge_aria", {
                        count: unreadCount,
                      })}
                    >
                      {displayUnreadCount}
                    </span>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
