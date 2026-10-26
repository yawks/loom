import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import type { models } from "../../wailsjs/go/models";

export function confirmThreadMessage(
  current: models.Message[], message: models.Message, temporaryId?: string,
): models.Message[] {
  const messages = current.filter((item) => item.protocolMsgId !== temporaryId);
  return messages.some((item) => item.protocolMsgId === message.protocolMsgId)
    ? messages.map((item) => item.protocolMsgId === message.protocolMsgId ? message : item)
    : [...messages, message];
}

export function updateCachedMessage(
  client: QueryClient,
  conversationId: string,
  messageId: string,
  body: string,
  isEdited: boolean,
) {
  const update = (message: models.Message): models.Message => message.protocolMsgId === messageId
    ? { ...message, body, isEdited } as models.Message
    : message;
  client.setQueryData<InfiniteData<models.Message[]>>(
    ["messages", conversationId],
    (data) => data && { ...data, pages: data.pages.map((page) => page.map(update)) }
  );
  client.setQueriesData<models.Message[]>(
    { queryKey: ["threads", conversationId] },
    (data) => data?.map(update)
  );
}
