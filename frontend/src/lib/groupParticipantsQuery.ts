import { queryOptions } from "@tanstack/react-query";
import { GetGroupParticipants } from "../../wailsjs/go/main/App";
import type { models } from "../../wailsjs/go/models";

export const EMPTY_GROUP_PARTICIPANTS: models.GroupParticipant[] = [];

// Timeline enrichment and the participant sidebar share a single canonical
// query. Identical responses retain their identity through structural sharing,
// avoiding repeated name lookups and renders when membership has not changed.
export const groupParticipantsQuery = (conversationId: string, isGroup: boolean) => queryOptions({
  queryKey: ["participantsData", conversationId, isGroup] as const,
  queryFn: async (): Promise<models.GroupParticipant[]> => {
    if (!conversationId || !isGroup) return EMPTY_GROUP_PARTICIPANTS;
    try {
      const result = await GetGroupParticipants(conversationId);
      return Array.isArray(result) ? result : EMPTY_GROUP_PARTICIPANTS;
    } catch (error) {
      console.error("Failed to get group participants:", error);
      return EMPTY_GROUP_PARTICIPANTS;
    }
  },
  staleTime: 15_000,
  refetchInterval: 15_000,
  refetchOnWindowFocus: true,
});
