import { ArrowDownAZ, Clock } from "lucide-react";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { useEffect, useMemo, useState } from "react";
import { useQueryClient, useSuspenseQuery, useQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { GetMetaContacts, GetContactAliases } from "../../wailsjs/go/main/App";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";

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
  const { data: contacts } = useSuspenseQuery<models.MetaContact[], Error>({
    queryKey: ["metaContacts"],
    queryFn: fetchMetaContacts,
  });

  const { data: aliases = {} } = useQuery<Record<string, string>, Error>({
    queryKey: ["contactAliases"],
    queryFn: async () => {
      const aliasMap = await GetContactAliases();
      return aliasMap || {};
    },
  });

  // Listen for contact refresh events
  useEffect(() => {
    const unsubscribe = EventsOn("contacts-refresh", () => {
      // Invalidate and refetch contacts when sync completes or new message arrives
      queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
      queryClient.refetchQueries({ queryKey: ["metaContacts"], type: "active" });
    });

    return () => {
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [queryClient]);

  const contactsWithAliases = useMemo(() => {
    return contacts.map((contact) => {
      const alias = contact.linkedAccounts.find((acc) => aliases[acc.userId]);
      const displayName = alias ? aliases[alias.userId] : contact.displayName;
      return Object.assign({}, contact, { displayName });
    }) as models.MetaContact[];
  }, [aliases, contacts]);

  useEffect(() => {
    setMetaContacts(contactsWithAliases);
  }, [contactsWithAliases, setMetaContacts]);

  const sortedContacts = useMemo(() => {
    const sorted = [...contactsWithAliases];

    if (sortBy === "alphabetical") {
      sorted.sort((a, b) =>
        a.displayName.localeCompare(b.displayName, undefined, {
          sensitivity: "base",
        })
      );
    } else if (sortBy === "last_message") {
      sorted.sort((a, b) => {
        const timeA = new Date(a.updatedAt || a.createdAt).getTime();
        const timeB = new Date(b.updatedAt || b.createdAt).getTime();
        return timeB - timeA;
      });
    }

    return sorted;
  }, [contactsWithAliases, sortBy]);

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
    });
    return counts;
  }, [readStateByConversation, sortedContacts]);

  if (contacts.length === 0) {
    return (
      <div className="flex flex-col h-full">
        <div className="p-2 border-b">
          <h2 className="text-lg font-semibold">{t("contacts")}</h2>
        </div>
        <div className="flex flex-col items-center justify-center flex-1 text-muted-foreground">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary mb-2"></div>
          <p className="text-sm">Loading conversations...</p>
        </div>
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
                <Avatar>
                  <AvatarImage src={contact.avatarUrl} alt={contact.displayName} />
                  <AvatarFallback>
                    {contact.displayName.substring(0, 2).toUpperCase()}
                  </AvatarFallback>
                </Avatar>
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
