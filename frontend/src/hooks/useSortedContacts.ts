import { GetAllLastMessageTimestamps, GetAllLastMessages, GetContactAliases } from "../../wailsjs/go/main/App";

import type { models } from "../../wailsjs/go/models";
import { timeToDate } from "@/lib/utils";
import { useAppStore } from "@/lib/store";
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";

type SortOption = "alphabetical" | "last_message" | "unread";

export function useSortedContacts(sortBy: SortOption = "last_message") {
  const metaContacts = useAppStore((state) => state.metaContacts);
  const { data: aliases = {} } = useQuery<Record<string, string>, Error>({
    queryKey: ["contactAliases"],
    queryFn: async () => {
      const aliasMap = await GetContactAliases();
      return aliasMap || {};
    },
  });

  const contactsWithAliases = useMemo(() => {
    return metaContacts.map((contact) => {
      const alias = contact.linkedAccounts.find((acc) => aliases[acc.userId]);
      const displayName = alias ? aliases[alias.userId] : contact.displayName;
      return Object.assign({}, contact, { displayName });
    }) as models.MetaContact[];
  }, [aliases, metaContacts]);

  // Récupérer tous les timestamps des derniers messages en une seule requête
  // C'est beaucoup plus efficace que de faire une requête par conversation
  const { data: allLastMessageTimestamps = {} } = useQuery<Record<string, any>, Error>({
    queryKey: ["allLastMessageTimestamps"],
        queryFn: async () => {
          try {
        const timestamps = await GetAllLastMessageTimestamps();
        return timestamps || {};
      } catch (error) {
        console.error("Error fetching all last message timestamps:", error);
        return {};
      }
    },
    enabled: sortBy === "last_message",
    staleTime: 30000, // Cache pendant 30 secondes
            });
            
  // Récupérer tous les derniers messages complets en une seule requête
  // C'est beaucoup plus efficace que de faire une requête par conversation
  const { data: allLastMessages = {} } = useQuery<Record<string, models.Message | null>, Error>({
    queryKey: ["allLastMessages"],
    queryFn: async () => {
      try {
        const messages = await GetAllLastMessages();
        // Convert map to Record format
        const result: Record<string, models.Message | null> = {};
        for (const [conversationId, message] of Object.entries(messages)) {
          result[conversationId] = message || null;
            }
        return result;
          } catch (error) {
        console.error("Error fetching all last messages:", error);
        return {};
          }
        },
    enabled: sortBy === "last_message",
        staleTime: 30000, // Cache pendant 30 secondes
  });

  // Créer un map des dates du dernier message par conversation ID
  // Utilise les timestamps récupérés en une seule requête
  const lastMessageDates = useMemo(() => {
    const dates: Record<string, Date> = {};
    if (allLastMessageTimestamps) {
      for (const [conversationId, timestamp] of Object.entries(allLastMessageTimestamps)) {
        if (timestamp) {
          dates[conversationId] = timeToDate(timestamp);
      }
      }
    }
    return dates;
  }, [allLastMessageTimestamps]);

  // Créer un map des derniers messages par conversation ID
  // Utilise les messages récupérés en une seule requête
  const lastMessages = useMemo(() => {
    const messages: Record<string, models.Message | null> = {};
    if (allLastMessages) {
      for (const [conversationId, message] of Object.entries(allLastMessages)) {
        messages[conversationId] = message || null;
      }
    }
    return messages;
  }, [allLastMessages]);

  const sortedContacts = useMemo(() => {
    let sorted = [...contactsWithAliases];

    if (sortBy === "alphabetical") {
      sorted.sort((a, b) =>
        a.displayName.localeCompare(b.displayName, undefined, {
          sensitivity: "base",
        })
      );
    } else if (sortBy === "last_message") {
      // Filtrer pour ne garder que les conversations qui ont des messages/événements
      sorted = sorted.filter((contact) => {
        const conversationId = contact.linkedAccounts[0]?.userId ?? "";
        return conversationId && lastMessageDates[conversationId] !== undefined;
      });

      // Trier par date du dernier événement (message ou réaction) - plus récent en premier
      sorted.sort((a, b) => {
        const conversationIdA = a.linkedAccounts[0]?.userId ?? "";
        const conversationIdB = b.linkedAccounts[0]?.userId ?? "";
        
        const timeA = lastMessageDates[conversationIdA]?.getTime() ?? 0;
        const timeB = lastMessageDates[conversationIdB]?.getTime() ?? 0;
        
        return timeB - timeA;
      });
    }

    return sorted;
  }, [contactsWithAliases, sortBy, lastMessageDates]);

  return { sortedContacts, lastMessages };
}

