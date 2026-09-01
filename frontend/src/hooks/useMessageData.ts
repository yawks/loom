import { GetCurrentUserID, GetMessagesForConversation, GetMessagesForConversationBefore, GetParticipantNames, GetGroupParticipants, GetThreadSummaries, FetchLinkPreview } from "../../wailsjs/go/main/App";
import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";

import { getMessageDomId, normalizeSerializedQuotedReply } from "@/lib/messageUtils";
import { models } from "../../wailsjs/go/models";
import { timeToDate, extractFirstUrl } from "@/lib/utils";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { mergePendingMessages } from "@/lib/pendingMessages";

// SQLite is the source of truth. Keep a small LRU window of conversation
// histories in the renderer so browsing many conversations cannot grow the
// React Query cache for the lifetime of the application.
const MAX_CACHED_CONVERSATIONS = 8;

const fetchMessages = async (conversationID: string, beforeTimestamp?: Date): Promise<models.Message[]> => {
  try {
    const result = beforeTimestamp
      ? await GetMessagesForConversationBefore(conversationID, beforeTimestamp)
      : await GetMessagesForConversation(conversationID);
    const messages = Array.isArray(result) ? result : [];
    // For first-page fetches, merge any messages received via event that
    // haven't been committed to the DB yet (avoids a background-refetch race).
    return beforeTimestamp ? messages : mergePendingMessages(conversationID, messages);
  } catch (error) {
    console.error("Error fetching messages:", error);
    return [];
  }
};

export function useMessageData(
  conversationId: string,
  isGroupFromProvider: boolean,
  readPolicy?: { cursorAuthoritativeForNewMessages: boolean; ownActivityAdvancesBoundary: boolean },
) {
  const queryClient = useQueryClient();
  const syncConversation = useMessageReadStore((state) => state.syncConversation);
  const cleanupObsoleteMessages = useMessageReadStore((state) => state.cleanupObsoleteMessages);
  const readByConversation = useMessageReadStore((state) => state.readByConversation);
  const [participantNames, setParticipantNames] = useState<Map<string, string>>(new Map());
  const [groupParticipants, setGroupParticipants] = useState<models.GroupParticipant[]>([]);

  const conversationReadState = useMemo(
    () => readByConversation[conversationId] ?? {},
    [readByConversation, conversationId]
  );

  const { data, fetchNextPage, hasNextPage, isFetchingNextPage, isLoading, isFetching } =
    useInfiniteQuery<models.Message[], Error>({
      queryKey: ["messages", conversationId],
      queryFn: ({ pageParam }) => {
        const beforeTimestamp = pageParam ? new Date(pageParam as string) : undefined;
        return fetchMessages(conversationId, beforeTimestamp);
      },
      enabled: !!conversationId,
      initialData: { pages: [], pageParams: [] },
      // Bound an opened conversation to 500 messages. The visible list is
      // virtualised, but query data itself otherwise grows for the session.
      maxPages: 10,
      staleTime: 0,
      getNextPageParam: (lastPage, allPages) => {
        if (!lastPage || !Array.isArray(lastPage) || lastPage.length === 0) return undefined;
        if (!allPages || !Array.isArray(allPages)) return undefined;

        const allMessages = allPages.flat();
        if (allMessages.length === 0) return undefined;

        const oldestMessage = allMessages.reduce((oldest, msg) => {
          const msgTime = timeToDate(msg.timestamp);
          const oldestTime = timeToDate(oldest.timestamp);
          return msgTime < oldestTime ? msg : oldest;
        });

        if (lastPage.length < 50) return undefined;
        return timeToDate(oldestMessage.timestamp).toISOString();
      },
      initialPageParam: undefined,
    });

  useEffect(() => {
    if (!conversationId) return;

    const olderConversationQueries = queryClient
      .getQueryCache()
      .findAll({ queryKey: ["messages"] })
      .filter((query) =>
        query.queryKey.length === 2 &&
        typeof query.queryKey[1] === "string" &&
        query.queryKey[1] !== conversationId
      )
      .sort((a, b) => a.state.dataUpdatedAt - b.state.dataUpdatedAt);

    // One slot is reserved for the active conversation. Evict all associated
    // detail/thread data for the least recently used histories as well.
    const overflow = olderConversationQueries.slice(MAX_CACHED_CONVERSATIONS - 1);
    for (const query of overflow) {
      const oldConversationId = query.queryKey[1] as string;
      queryClient.removeQueries({ queryKey: ["messages", oldConversationId], exact: true });
      queryClient.removeQueries({ queryKey: ["messages-details", oldConversationId], exact: true });
      queryClient.removeQueries({ queryKey: ["threads", oldConversationId] });
    }
  }, [conversationId, queryClient]);

  const messages = useMemo(() => {
    if (!data?.pages || !Array.isArray(data.pages)) return [];
    const flat = data.pages.filter((page) => Array.isArray(page)).flat().map(normalizeSerializedQuotedReply);
    const seen = new Set<string>();
    return flat.filter((msg) => {
      const id = msg.protocolMsgId;
      if (id && seen.has(id)) return false;
      if (id) seen.add(id);
      return true;
    });
  // React Query's structural sharing keeps `data` stable for identical
  // refetches. Depending on it directly avoids serializing the complete
  // history before every render, including the latency-sensitive optimistic
  // insertion performed when the user sends a message.
  }, [data]);

  const isGroupConversation = useMemo(() => {
    if (isGroupFromProvider) return true;
    if (messages.length > 0) {
      const uniqueSenders = new Set(
        messages.map((m) => (m.senderId ? m.senderId.replace(/:[0-9]+(@|$)/, "$1") : ""))
      );
      return uniqueSenders.size > 2;
    }
    return false;
  }, [isGroupFromProvider, messages]);

  const { mainMessages, threadsByParent } = useMemo(() => {
    const main: models.Message[] = [];
    const threads: Record<string, models.Message[]> = {};

    messages.forEach((msg) => {
      const hasBody = msg.body && msg.body.trim() !== "";
      const hasAttachments = msg.attachments && msg.attachments.trim() !== "";
      const isCallMessage = msg.callType && msg.callType.trim() !== "";
      if (!hasBody && !hasAttachments && !isCallMessage) return;

      const isThreadReply = msg.threadId && msg.threadId !== msg.protocolMsgId;

      if (!msg.threadId || msg.threadId === msg.protocolMsgId) {
        main.push(msg);
      } else if (isThreadReply) {
        if (!threads[msg.threadId]) threads[msg.threadId] = [];
        threads[msg.threadId].push(msg);
      }
    });

    const sortedMain = [...main].sort(
      (a, b) => timeToDate(a.timestamp).getTime() - timeToDate(b.timestamp).getTime()
    );

    return { mainMessages: sortedMain, threadsByParent: threads };
  }, [messages]);

  const threadParentIds = useMemo(
    () => mainMessages
      .filter((message) => message.protocolMsgId && message.threadReplyCount > 0)
      .map((message) => message.protocolMsgId),
    [mainMessages]
  );
  const { data: threadSummaries = [] } = useQuery<models.ThreadSummary[]>({
    queryKey: ["thread-summaries", conversationId, threadParentIds.join(",")],
    queryFn: () => GetThreadSummaries(conversationId, threadParentIds),
    enabled: Boolean(conversationId && threadParentIds.length > 0),
  });
  const threadReplyCounts = useMemo(
    () => Object.fromEntries(threadSummaries.map((summary) => [summary.parentMessageId, summary.replyCount])),
    [threadSummaries]
  );
  const threadParticipantsByParent = useMemo(
    () => Object.fromEntries(threadSummaries.map((summary) => [summary.parentMessageId, summary.participants ?? []])),
    [threadSummaries]
  );

  const { data: providerCurrentUserId } = useQuery<string>({
    queryKey: ["current-user-id", conversationId],
    queryFn: () => GetCurrentUserID(conversationId),
    enabled: Boolean(conversationId),
    staleTime: Infinity,
  });
  const currentUserId = providerCurrentUserId || messages.find((msg) => msg.isFromMe && msg.senderId)?.senderId;

  useEffect(() => {
    if (!conversationId) {
      setGroupParticipants([]);
      return;
    }
    if (isGroupFromProvider || isGroupConversation) {
      GetGroupParticipants(conversationId)
        .then((res) => {
          if (Array.isArray(res)) {
            setGroupParticipants(res);
          }
        })
        .catch((err) => {
          console.error("Failed to load group participants:", err);
        });
    } else {
      setGroupParticipants([]);
    }
  }, [conversationId, isGroupFromProvider, isGroupConversation]);

  useEffect(() => {
    if (!conversationId) return;
    const loadParticipantNames = async () => {
      try {
        const userIds = new Set<string>();
        messages.forEach((msg) => {
          if (msg.senderId) userIds.add(msg.senderId);
          if (msg.reactions) msg.reactions.forEach((r) => userIds.add(r.userId));
          if (msg.receipts) msg.receipts.forEach((rcpt) => userIds.add(rcpt.userId));
        });
        groupParticipants.forEach((p) => {
          if (p.userId) userIds.add(p.userId);
        });
        if (userIds.size === 0) return;

        const normalizedIds = Array.from(userIds);
        const names = await GetParticipantNames(normalizedIds);
        const namesMap = new Map<string, string>();

        // Store all entries from names object and their normalized variants
        if (names) {
          for (const [key, value] of Object.entries(names)) {
            if (value && value.trim()) {
              namesMap.set(key, value);
              if (key.includes("::")) {
                const raw = key.split("::")[1];
                namesMap.set(raw, value);
              }
            }
          }
        }

        Array.from(userIds).forEach((originalId, index) => {
          const normalizedId = normalizedIds[index];
          const name = names?.[normalizedId] || names?.[originalId];
          if (name) {
            namesMap.set(originalId, name);
            namesMap.set(normalizedId, name);
          }
        });

        // In a direct conversation, the message's scoped sender name is more
        // authoritative than a global account lookup. Remote participant IDs
        // are not guaranteed to be globally unique across provider instances.
        const preferMessageSenderName = !isGroupFromProvider;
        messages.forEach((msg) => {
          if (
            msg.senderId &&
            msg.senderName?.trim() &&
            (preferMessageSenderName || !namesMap.has(msg.senderId))
          ) {
            namesMap.set(msg.senderId, msg.senderName);
          }
        });

        setParticipantNames(namesMap);
      } catch (error) {
        console.error("Failed to load participant names:", error);
      }
    };
    loadParticipantNames();
  }, [conversationId, isGroupFromProvider, messages, groupParticipants]);

  useEffect(() => {
    if (!conversationId || messages.length === 0 || !readPolicy) return;
    syncConversation(conversationId, messages, currentUserId, readPolicy);
    const allMessageIds = new Set(messages.map((msg) => getMessageDomId(msg)));
    cleanupObsoleteMessages(conversationId, allMessageIds);
  }, [conversationId, messages, currentUserId, readPolicy, syncConversation, cleanupObsoleteMessages]);

  useEffect(() => {
    if (messages.length > 0 && !isLoading) {
      queryClient.invalidateQueries({ queryKey: ["allLastMessageTimestamps"] });
      queryClient.invalidateQueries({ queryKey: ["allLastMessages"] });
      queryClient.invalidateQueries({ queryKey: ["lastMessage"] });
    }
  }, [messages.length, isLoading, queryClient]);

  // Pre-fetch link previews so the cache is warm before Virtuoso renders the items.
  // Without this, previews appear after render and cause a Virtuoso layout shift (flicker).
  useEffect(() => {
    for (const msg of mainMessages) {
      if (!msg.body) continue;
      const url = extractFirstUrl(msg.body);
      if (!url) continue;
      const key = ["link-preview", url];
      if (queryClient.getQueryData(key) !== undefined) continue;
      queryClient.prefetchQuery({
        queryKey: key,
        queryFn: () => FetchLinkPreview(url),
        staleTime: 60 * 60 * 1000,
      });
    }
  // mainMessages stays stable while React Query's data reference is unchanged.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mainMessages]);

  return {
    messages,
    mainMessages,
    threadsByParent,
    threadReplyCounts,
    threadParticipantsByParent,
    isGroupConversation,
    currentUserId,
    groupParticipants,
    participantNames,
    conversationReadState,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
    isLoading,
    isFetching,
    data,
  };
}
