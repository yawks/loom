import { useMemo } from "react";
import { useAppStore } from "@/lib/store";
import { useQuery, useQueries } from "@tanstack/react-query";
import { GetMessagesForConversation, GetContactAliases } from "../../wailsjs/go/main/App";
import { timeToDate } from "@/lib/utils";
import type { models } from "../../wailsjs/go/models";

type SortOption = "alphabetical" | "last_message";

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

  // Récupérer le dernier message affiché (non vide) de chaque conversation pour obtenir la date réelle
  const lastMessagesQueries = useQueries({
    queries: contactsWithAliases.map((contact) => {
      const conversationId = contact.linkedAccounts[0]?.userId ?? "";
      return {
        queryKey: ["lastMessage", conversationId],
        queryFn: async () => {
          if (!conversationId) return null;
          try {
            const messages = await GetMessagesForConversation(conversationId);
            // Filtrer les messages vides (comme dans MessageList)
            const displayedMessages = (messages || []).filter((msg) => {
              const hasBody = msg.body && msg.body.trim() !== "";
              const hasAttachments = msg.attachments && msg.attachments.trim() !== "";
              return hasBody || hasAttachments;
            });
            
            // Retourner le message le plus récent parmi les messages affichés
            if (displayedMessages.length > 0) {
              // Trier par timestamp décroissant pour obtenir le plus récent en premier
              const sorted = [...displayedMessages].sort((a, b) => {
                const timeA = timeToDate(a.timestamp).getTime();
                const timeB = timeToDate(b.timestamp).getTime();
                return timeB - timeA; // Décroissant
              });
              return sorted[0]; // Le premier est le plus récent
            }
            return null;
          } catch (error) {
            console.error(`Error fetching last message for ${conversationId}:`, error);
            return null;
          }
        },
        enabled: !!conversationId && sortBy === "last_message",
        staleTime: 30000, // Cache pendant 30 secondes
      };
    }),
  });

  // Créer un map des dates du dernier message par conversation ID
  const lastMessageDates = useMemo(() => {
    const dates: Record<string, Date> = {};
    lastMessagesQueries.forEach((query, index) => {
      const contact = contactsWithAliases[index];
      const conversationId = contact.linkedAccounts[0]?.userId ?? "";
      if (query.data && conversationId) {
        dates[conversationId] = timeToDate(query.data.timestamp);
      }
    });
    return dates;
  }, [lastMessagesQueries, contactsWithAliases]);

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
        const conversationIdA = a.linkedAccounts[0]?.userId ?? "";
        const conversationIdB = b.linkedAccounts[0]?.userId ?? "";
        
        // Utiliser la date du dernier message si disponible, sinon fallback sur updatedAt/createdAt
        const timeA = lastMessageDates[conversationIdA] 
          ? lastMessageDates[conversationIdA].getTime()
          : timeToDate(a.updatedAt || a.createdAt).getTime();
        const timeB = lastMessageDates[conversationIdB]
          ? lastMessageDates[conversationIdB].getTime()
          : timeToDate(b.updatedAt || b.createdAt).getTime();
        
        return timeB - timeA;
      });
    }

    return sorted;
  }, [contactsWithAliases, sortBy, lastMessageDates]);

  return sortedContacts;
}

