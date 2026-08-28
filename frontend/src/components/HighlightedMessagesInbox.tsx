import { useMemo } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { BadgeAlert, Loader2 } from "lucide-react";

import { GetConfiguredProviders, GetHighlightedMessages } from "../../wailsjs/go/main/App";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { MessageText } from "./MessageText";
import { ProtocolIcon } from "./ProtocolIcon";
import { normalizeSerializedQuotedReply } from "@/lib/messageUtils";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

export function HighlightedMessagesInbox() {
  const { t } = useTranslation();
  const contacts = useAppStore((state) => state.metaContacts);
  const readByConversation = useMessageReadStore((state) => state.readByConversation);
  const setSelectedContact = useAppStore((state) => state.setSelectedContact);
  const setSelectedProviderFilter = useAppStore((state) => state.setSelectedProviderFilter);
  const setMessageSearchTargetId = useAppStore((state) => state.setMessageSearchTargetId);
  const setSelectedThreadId = useAppStore((state) => state.setSelectedThreadId);
  const setUnreadNavigationTarget = useAppStore((state) => state.setUnreadNavigationTarget);
  const { data: configuredProviders = [] } = useQuery({
    queryKey: ["configuredProviders"],
    queryFn: () => GetConfiguredProviders().catch(() => []),
  });
  const providerNames = useMemo(() => new Map(configuredProviders.map((provider) => [
    provider.instanceId || provider.id,
    provider.instanceName || provider.name,
  ])), [configuredProviders]);
  const inbox = useInfiniteQuery({
    queryKey: ["highlightedMessages"],
    queryFn: ({ pageParam }) => GetHighlightedMessages(pageParam as number),
    initialPageParam: 0,
    getNextPageParam: (lastPage, pages) => lastPage.hasMore ? pages.length * 15 : undefined,
    retry: false,
  });
  const results = useMemo(() => inbox.data?.pages.flatMap((page) => page.items ?? []) ?? [], [inbox.data]);

  if (inbox.isLoading) return <div className="flex justify-center py-12"><Loader2 className="h-5 w-5 animate-spin" /></div>;
  if (inbox.isError) return <div className="px-4 py-12 text-center text-sm text-destructive">{t("watchlist_error")}</div>;
  if (results.length === 0) return (
    <div className="flex flex-col items-center px-4 py-12 text-center text-sidebar-muted-foreground">
      <BadgeAlert className="mb-4 h-12 w-12 opacity-30" />
      <p className="text-sm">{t("watchlist_empty")}</p>
    </div>
  );

  return (
    <div className="highlighted-messages-inbox space-y-1 px-1 py-2">
      {results.map((result, index) => {
        const contact = contacts.find((item) => item.id === result.metaContactId);
        const message = normalizeSerializedQuotedReply(result.message);
        const isUnread = readByConversation[message.protocolConvId]?.[message.protocolMsgId] === false;
        return (
          <button
            key={`${message.protocolMsgId}-${index}`}
            className={cn(
              "w-full rounded-lg border px-2 py-2 text-left transition-colors hover:border-amber-400/40 hover:bg-sidebar-hover focus:outline-none focus:ring-2 focus:ring-amber-500",
              isUnread
                ? "border-amber-400/50 bg-amber-400/10 text-sidebar-foreground"
                : "border-transparent"
            )}
            onClick={() => {
              if (!contact) return;
              if (message.threadId) {
                setSelectedThreadId(message.threadId);
                setUnreadNavigationTarget({
                  conversationId: message.protocolConvId,
                  messageId: message.protocolMsgId,
                  threadId: message.threadId,
                });
              } else {
                setSelectedThreadId(null);
                setUnreadNavigationTarget(null);
                setMessageSearchTargetId(message.protocolMsgId);
              }
              setSelectedProviderFilter(result.providerInstanceId);
              setSelectedContact(contact);
            }}
          >
            <div className="mb-1.5 flex items-center gap-2">
              <Avatar className="h-7 w-7">
                <AvatarImage src={result.conversationAvatar} />
                <AvatarFallback>{result.conversationName.slice(0, 2).toUpperCase()}</AvatarFallback>
              </Avatar>
              <div className="min-w-0 flex-1">
                <div className={cn("truncate text-sm", isUnread ? "font-bold" : "font-medium")}>{result.conversationName}</div>
                <div className="flex items-center gap-1 truncate text-[11px] text-sidebar-muted-foreground">
                  <ProtocolIcon protocol={result.protocol} size={11} />
                  <span className="truncate">{providerNames.get(result.providerInstanceId) || result.protocol}</span>
                  <span aria-hidden="true">·</span>
                  <time>{new Date(message.timestamp as unknown as string).toLocaleString([], { dateStyle: "short", timeStyle: "short" })}</time>
                </div>
              </div>
            </div>
            <div className={cn("line-clamp-3 text-sm", isUnread ? "font-semibold text-sidebar-foreground" : "text-sidebar-muted-foreground")}>
              <MessageText text={message.body} providerInstanceId={result.providerInstanceId} emojiSize={14} preview isFromMe={message.isFromMe} />
            </div>
          </button>
        );
      })}
      {inbox.hasNextPage && (
        <Button variant="ghost" size="sm" className="w-full" disabled={inbox.isFetchingNextPage} onClick={() => void inbox.fetchNextPage()}>
          {inbox.isFetchingNextPage ? <Loader2 className="h-4 w-4 animate-spin" /> : t("load_more")}
        </Button>
      )}
    </div>
  );
}
