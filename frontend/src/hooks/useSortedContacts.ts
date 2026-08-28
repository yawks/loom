import { GetAllLastMessageTimestamps, GetAllLastMessages, GetContactAliases } from "../../wailsjs/go/main/App";

import type { models } from "../../wailsjs/go/models";
import { timeToDate } from "@/lib/utils";
import { useAppStore } from "@/lib/store";
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";


type SortOption = "alphabetical" | "last_message" | "unread";

export function useSortedContacts(sortBy: SortOption = "last_message") {
  const metaContacts = useAppStore((state) => state.metaContacts);
  const selectedProviderFilter = useAppStore((state) => state.selectedProviderFilter);
  const { data: aliases = {} } = useQuery<Record<string, string>, Error>({
    queryKey: ["contactAliases"],
    queryFn: async () => {
      const aliasMap = await GetContactAliases();
      return aliasMap || {};
    },
  });

  const contactsWithAliases = useMemo(() => {
    return metaContacts.map((contact) => {
      const accounts = contact.linkedAccounts ?? [];
      const alias = accounts.find((acc) => aliases[acc.userId]);
      const displayName = alias ? aliases[alias.userId] : contact.displayName;
      return Object.assign({}, contact, { displayName, linkedAccounts: accounts });
    }) as models.MetaContact[];
  }, [aliases, metaContacts]);

  // Récupérer tous les timestamps des derniers messages en une seule requête
  // Toujours activé pour que les données soient disponibles immédiatement lors du basculement vers "Récents"
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
    staleTime: 30000, // Cache pendant 30 secondes
    placeholderData: (previousData) => previousData,
  });

  // Récupérer tous les derniers messages complets en une seule requête
  const { data: allLastMessages = {} } = useQuery<Record<string, models.Message | null>, Error>({
    queryKey: ["allLastMessages"],
    queryFn: async () => {
      try {
        const messages = await GetAllLastMessages();
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
    staleTime: 30000, // Cache pendant 30 secondes
    placeholderData: (previousData) => previousData,
  });

  // Créer un map des dates du dernier message par conversation ID
  // Utilise les timestamps récupérés en une seule requête
  const lastMessageDates = useMemo(() => {
    const dates: Record<string, Date> = {};
    if (allLastMessageTimestamps) {
      for (const [conversationId, timestamp] of Object.entries(allLastMessageTimestamps)) {
        if (timestamp) {
          // Newer backends return Unix milliseconds; accept legacy Unix seconds
          // as well as ISO strings kept by optimistic updates.
          dates[conversationId] =
            typeof timestamp === "number"
              ? new Date(timestamp > 10_000_000_000 ? timestamp : timestamp * 1000)
              : timeToDate(timestamp);
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

    // When a provider filter is active, only consider accounts from that provider.
    // Without this, a contact with a recent message on provider B would appear in
    // provider A's "Recent" tab because its max timestamp comes from provider B.
    const getAccountsForTime = (contact: models.MetaContact): models.LinkedAccount[] => {
      const accounts = contact.linkedAccounts ?? [];
      if (!selectedProviderFilter) return accounts;
      const filtered = accounts.filter((acc) => acc.providerInstanceId === selectedProviderFilter);
      return filtered.length > 0 ? filtered : accounts;
    };

    // Helper function to get the timestamp for sorting.
    const getContactTime = (contact: models.MetaContact): number => {
      let maxTime = 0;
      for (const acc of getAccountsForTime(contact)) {
        // Try both conversationId and userId because remote services may use separate identifiers.
        // vs normalized U-prefix), and we want the best match in lastMessageDates.
        const idsToCheck: string[] = [];
        if (acc.conversationId) idsToCheck.push(acc.conversationId);
        if (acc.userId && acc.userId !== acc.conversationId) idsToCheck.push(acc.userId);
        for (const id of idsToCheck) {
          if (lastMessageDates[id]) {
            const time = lastMessageDates[id].getTime();
            if (time > maxTime) maxTime = time;
          }
        }
      }
      return maxTime;
    };

    if (sortBy === "alphabetical") {
      sorted.sort((a, b) =>
        a.displayName.localeCompare(b.displayName, undefined, {
          sensitivity: "base",
        })
      );
    } else if (sortBy === "last_message" || sortBy === "unread") {
      // Sort by last message/reaction date - most recent first
      // Also apply this sorting to the "unread" tab for better UX
      sorted.sort((a, b) => getContactTime(b) - getContactTime(a));

      // Filter out contacts with no messages only for the "recent" tab
      if (sortBy === "last_message") {
        sorted = sorted.filter((contact) => getContactTime(contact) > 0);
      }
    }

    return sorted;
  }, [contactsWithAliases, sortBy, lastMessageDates, selectedProviderFilter]);

  return { sortedContacts, lastMessages };
}
