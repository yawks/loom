import { ArrowDownAZ, Clock } from "lucide-react";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { useEffect, useMemo, useState } from "react";
import { useQueryClient, useSuspenseQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { GetMetaContacts } from "../../wailsjs/go/main/App";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";

type SortOption = "alphabetical" | "last_message";

// Wrapper function to use Wails with React Query's suspense mode
const fetchMetaContacts = async () => {
  return GetMetaContacts();
};

export function ContactList() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const setSelectedContact = useAppStore((state) => state.setSelectedContact);
  const [sortBy, setSortBy] = useState<SortOption>("last_message");
  const { data: contacts } = useSuspenseQuery<models.MetaContact[], Error>({
    queryKey: ["metaContacts"],
    queryFn: fetchMetaContacts,
  });

  // Listen for contact refresh events
  useEffect(() => {
    const unsubscribe = EventsOn("contacts-refresh", () => {
      // Invalidate and refetch contacts when sync completes
      queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
    });

    return () => {
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [queryClient]);

  // Sort contacts based on selected option
  const sortedContacts = useMemo(() => {
    const sorted = [...contacts];
    
    if (sortBy === "alphabetical") {
      sorted.sort((a, b) => 
        a.displayName.localeCompare(b.displayName, undefined, { sensitivity: "base" })
      );
    } else if (sortBy === "last_message") {
      // Sort by last message timestamp (most recent first)
      // For now, we'll use updatedAt as a proxy for last message time
      // TODO: Use actual last message timestamp when available
      sorted.sort((a, b) => {
        const timeA = new Date(a.updatedAt || a.createdAt).getTime();
        const timeB = new Date(b.updatedAt || b.createdAt).getTime();
        return timeB - timeA; // Most recent first
      });
    }
    
    return sorted;
  }, [contacts, sortBy]);

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
        <h2 className="text-lg font-semibold">{t("contacts")}</h2>
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
          {sortedContacts.map((contact) => (
            <div
              key={contact.id}
              className="flex items-center space-x-3 p-2 rounded-lg hover:bg-muted cursor-pointer"
              onClick={() => setSelectedContact(contact)}
            >
              <Avatar>
                <AvatarImage src={contact.avatarUrl} alt={contact.displayName} />
                <AvatarFallback>
                  {contact.displayName.substring(0, 2).toUpperCase()}
                </AvatarFallback>
              </Avatar>
              <span className="font-medium">{contact.displayName}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
