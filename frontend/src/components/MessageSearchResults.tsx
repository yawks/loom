import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { GetConfiguredProviders, SearchMessages, SearchMessagesInConversation } from "../../wailsjs/go/main/App";
import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Loader2 } from "lucide-react";
import { MessageText } from "./MessageText";
import { ProtocolIcon } from "./ProtocolIcon";
import { normalizeSerializedQuotedReply } from "@/lib/messageUtils";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";
import { timeToDate } from "@/lib/utils";

export function MessageSearchResults({
  query,
  onResultSelected,
  metaContactId,
  debounceMs = 3000,
}: {
  query: string;
  onResultSelected: () => void;
  metaContactId?: number;
  debounceMs?: number;
}) {
  const { t, i18n } = useTranslation();
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [showAllResults, setShowAllResults] = useState(false);
  const trimmedQuery = query.trim();
  const contacts = useAppStore((state) => state.metaContacts);
  const setSelectedContact = useAppStore((state) => state.setSelectedContact);
  const setSelectedProviderFilter = useAppStore((state) => state.setSelectedProviderFilter);
  const setMessageSearchTargetId = useAppStore((state) => state.setMessageSearchTargetId);
  const { data: configuredProviders = [] } = useQuery({
    queryKey: ["configuredProviders"],
    queryFn: () => GetConfiguredProviders().catch(() => []),
  });
  const providerNames = useMemo(
    () => new Map(
      configuredProviders.map((provider) => [
        provider.instanceId || provider.id,
        provider.instanceName || provider.name,
      ])
    ),
    [configuredProviders]
  );

  useEffect(() => {
    const trimmed = query.trim();
    // Reset prior result state whenever a distinct search starts.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDebouncedQuery("");
    setShowAllResults(false);
    if (trimmed.length < 3) return;
    if (debounceMs === 0) {
      setDebouncedQuery(trimmed);
      return;
    }
    const timer = setTimeout(() => setDebouncedQuery(trimmed), debounceMs);
    return () => clearTimeout(timer);
  }, [debounceMs, query]);

  const search = useInfiniteQuery({
    queryKey: ["messageSearch", debouncedQuery, metaContactId ?? "all"],
    queryFn: ({ pageParam }) => metaContactId
      ? SearchMessagesInConversation(debouncedQuery, pageParam as number, metaContactId)
      : SearchMessages(debouncedQuery, pageParam as number),
    initialPageParam: 0,
    getNextPageParam: (lastPage, pages) => lastPage.hasMore ? pages.length * 15 : undefined,
    enabled: debouncedQuery.length >= 3,
    retry: false,
  });
  const results = useMemo(
    () => search.data?.pages.flatMap((page) => page.items ?? []) ?? [],
    [search.data]
  );
  const displayedResults = showAllResults ? results : results.slice(0, 5);

  if (trimmedQuery.length < 3) return null;

  if (debouncedQuery.length < 3) {
    return (
      <section className="message-search-results border-t pt-4">
        <h3 className="message-search-results__title pb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {t("messages")}
        </h3>
        <div className="message-search-results__waiting py-4 text-center text-sm text-muted-foreground">
          {t("message_search_waiting")}
        </div>
      </section>
    );
  }

  return (
    <section className="message-search-results space-y-1 border-t pt-4">
      <h3 className="message-search-results__title pb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        {t("messages")}
      </h3>
      {search.isLoading && (
        <div className="message-search-results__loading flex justify-center py-4">
          <Loader2 className="h-4 w-4 animate-spin" />
        </div>
      )}
      {search.isError && (
        <div className="message-search-results__error py-4 text-center text-sm text-destructive">
          {t("message_search_error")}
        </div>
      )}
      {!search.isLoading && !search.isError && results.length === 0 && (
        <div className="message-search-results__empty py-4 text-center text-sm text-muted-foreground">
          {t("message_search_no_results")}
        </div>
      )}
      {displayedResults.map((result, index) => {
        const previous = displayedResults[index - 1];
        const startsGroup = !previous || previous.metaContactId !== result.metaContactId;
        const contact = contacts.find((item) => item.id === result.metaContactId);
        const normalizedMessage = normalizeSerializedQuotedReply(result.message);
        const messageDate = timeToDate(result.message.timestamp);
        const formattedTimestamp = messageDate.toLocaleString(i18n.language, {
          dateStyle: "medium",
          timeStyle: "short",
        });

        return (
          <div className="message-search-results__group" key={`${result.message.protocolMsgId}-${index}`}>
            {startsGroup && metaContactId === undefined && (
              <div className="message-search-results__header flex items-center gap-2 px-2 pt-2">
                <Avatar className="h-8 w-8">
                  <AvatarImage src={result.conversationAvatar} />
                  <AvatarFallback>{result.conversationName.slice(0, 2).toUpperCase()}</AvatarFallback>
                </Avatar>
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium">{result.conversationName}</div>
                  <div className="flex items-center gap-1 truncate text-xs text-muted-foreground opacity-40">
                    <ProtocolIcon protocol={result.protocol} size={12} />
                    <span className="truncate">
                      {providerNames.get(result.providerInstanceId) || result.protocol}
                    </span>
                  </div>
                </div>
              </div>
            )}
            <button
              data-search-nav-item
              className="message-search-results__body mt-1 w-full rounded-lg border border-transparent px-3 py-2 text-left text-sm text-muted-foreground transition-colors hover:border-border hover:bg-muted hover:text-foreground focus:border-border focus:bg-muted focus:text-foreground focus:outline-none focus:ring-2 focus:ring-primary"
              onClick={() => {
                if (!contact) return;
                setMessageSearchTargetId(result.message.protocolMsgId);
                setSelectedProviderFilter(result.providerInstanceId);
                setSelectedContact(contact);
                onResultSelected();
              }}
            >
              <time
                dateTime={messageDate.toISOString()}
                className="mb-1 block text-xs text-muted-foreground"
                title={formattedTimestamp}
              >
                {formattedTimestamp}
              </time>
              <MessageText
                text={normalizedMessage.body}
                highlightQuery={debouncedQuery}
                providerInstanceId={result.providerInstanceId}
                emojiSize={14}
                preview
                isFromMe={normalizedMessage.isFromMe}
              />
            </button>
          </div>
        );
      })}
      {(results.length > 5 || search.hasNextPage) && (
        <button
          type="button"
          data-search-nav-item
          className="message-search-results__show-more mt-1 w-full rounded-lg border border-transparent px-3 py-2 text-center text-xs font-medium text-primary transition-colors hover:border-border hover:bg-muted focus:border-border focus:bg-muted focus:outline-none focus:ring-2 focus:ring-primary"
          onClick={() => setShowAllResults((showAll) => !showAll)}
        >
          {showAllResults ? t("show_less") : t("show_more")}
        </button>
      )}
      {showAllResults && search.hasNextPage && (
        <Button
          data-search-nav-item
          variant="ghost"
          size="sm"
          className="message-search-results__more mt-2 w-full border border-transparent text-xs hover:border-border hover:bg-muted focus:border-border focus:ring-2 focus:ring-primary"
          disabled={search.isFetchingNextPage}
          onClick={() => void search.fetchNextPage()}
        >
          {search.isFetchingNextPage
            ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
            : t("load_more")}
        </Button>
      )}
    </section>
  );
}
