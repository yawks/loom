import type { InfiniteData } from "@tanstack/react-query";
import type { models } from "../../wailsjs/go/models";

export function mergeMessageBatch(
  current: InfiniteData<models.Message[]> | undefined,
  messages: models.Message[],
  replacedMessageId?: string,
): InfiniteData<models.Message[]> | undefined {
  if (!current?.pages.length) return current;
  const incoming = new Map(messages.map((message) => [message.protocolMsgId, message]));
  const pages = current.pages.map((page) => page.filter((message) =>
    !replacedMessageId || message.protocolMsgId !== replacedMessageId
  ).map((message) => {
    const updated = incoming.get(message.protocolMsgId);
    incoming.delete(message.protocolMsgId);
    return updated ? { ...message, ...updated } as models.Message : message;
  }));
  pages[0] = [...pages[0], ...incoming.values()];
  return { ...current, pages };
}
