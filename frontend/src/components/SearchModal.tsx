import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { Input } from "@/components/ui/input";
import { Search } from "lucide-react";
import { Emoji } from "./Emoji";
import { ProtocolIcon } from "./ProtocolIcon";
import { MessageSearchResults } from "./MessageSearchResults";
import { cn, timeToDate } from "@/lib/utils";
import { getContactStatusEmoji } from "@/lib/statusEmoji";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";
import { GetConfiguredProviders, GetAllLastMessageTimestamps } from "../../wailsjs/go/main/App";

interface SearchModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type NavigationSection = "input" | "recently_viewed" | "recent_searches" | "search_results";

// Helpers for localStorage recent searches
const getStoredIds = (key: string): number[] => {
  if (typeof window === "undefined") return [];
  try {
    const item = window.localStorage.getItem(key);
    return item ? JSON.parse(item) : [];
  } catch {
    return [];
  }
};

const saveStoredIds = (key: string, ids: number[]) => {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(key, JSON.stringify(ids));
  } catch (error) {
    console.error(`Failed to save ${key}:`, error);
  }
};

export function SearchModal({ open, onOpenChange }: SearchModalProps) {
  const { t } = useTranslation();
  const [searchQuery, setSearchQuery] = useState("");
  const [navSection, setNavSection] = useState<NavigationSection>("input");
  const [recentlyViewedIndex, setRecentlyViewedIndex] = useState<number>(0);
  const [recentSearchesIndex, setRecentSearchesIndex] = useState<number>(0);
  const [searchResultsIndex, setSearchResultsIndex] = useState<number>(0);
  const [showAllContactResults, setShowAllContactResults] = useState(false);

  const [recentSearchIds, setRecentSearchIds] = useState<number[]>([]);
  const [recentlyViewedIds, setRecentlyViewedIds] = useState<number[]>([]);

  const inputRef = useRef<HTMLInputElement>(null);
  const selectedRecentlyViewedRef = useRef<HTMLDivElement>(null);
  const selectedRecentSearchesRef = useRef<HTMLDivElement>(null);
  const selectedSearchResultRef = useRef<HTMLDivElement>(null);
  const scrollContainerRef = useRef<HTMLDivElement>(null);

  const setSelectedContact = useAppStore((state) => state.setSelectedContact);
  const contacts = useAppStore((state) => state.metaContacts);
  const conversationHistory = useAppStore((state) => state.conversationHistory);

  const { data: configuredProviders = [] } = useQuery({
    queryKey: ["configuredProviders"],
    queryFn: () => GetConfiguredProviders().catch(() => []),
  });

  // Fetch all last message timestamps for recency calculation (like ContactList last_message sort)
  const { data: allLastMessageTimestamps = {} } = useQuery<Record<string, any>, Error>({
    queryKey: ["allLastMessageTimestamps"],
    queryFn: async () => {
      try {
        const timestamps = await GetAllLastMessageTimestamps();
        return timestamps || {};
      } catch (error) {
        console.error("Error fetching last message timestamps in SearchModal:", error);
        return {};
      }
    },
    staleTime: 30000,
  });

  const lastMessageDates = useMemo(() => {
    const dates: Record<string, number> = {};
    if (allLastMessageTimestamps) {
      for (const [conversationId, timestamp] of Object.entries(allLastMessageTimestamps)) {
        if (timestamp) {
          dates[conversationId] =
            typeof timestamp === "number"
              ? (timestamp > 10_000_000_000 ? timestamp : timestamp * 1000)
              : timeToDate(timestamp).getTime();
        }
      }
    }
    return dates;
  }, [allLastMessageTimestamps]);

  const providerNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const p of configuredProviders) {
      const id = p.instanceId || p.id;
      map.set(id, p.instanceName || p.name);
    }
    return map;
  }, [configuredProviders]);

  const providerProtocolById = useMemo(() => {
    const map = new Map<string, string>();
    for (const p of configuredProviders) {
      map.set(p.instanceId || p.id, p.id);
    }
    return map;
  }, [configuredProviders]);

  // Load persistent IDs when modal opens
  useEffect(() => {
    if (open) {
      setRecentSearchIds(getStoredIds("loom_recent_searches_ids"));
      setRecentlyViewedIds(getStoredIds("loom_recently_viewed_ids"));

      setTimeout(() => {
        inputRef.current?.focus();
        setSearchQuery("");
        setNavSection("input");
        setRecentlyViewedIndex(0);
        setRecentSearchesIndex(0);
        setSearchResultsIndex(0);
        setShowAllContactResults(false);
      }, 100);
    }
  }, [open]);

  // Map IDs to MetaContact objects
  const contactMap = useMemo(() => {
    const map = new Map<number, models.MetaContact>();
    for (const c of contacts) {
      map.set(c.id, c);
    }
    return map;
  }, [contacts]);

  // Compute activity timestamp for a contact (last message timestamp or recent visit)
  const getContactActivityTime = (contact: models.MetaContact): number => {
    let maxTime = 0;
    const accounts = contact.linkedAccounts ?? [];
    for (const acc of accounts) {
      const idsToCheck: string[] = [];
      if (acc.conversationId) idsToCheck.push(acc.conversationId);
      if (acc.userId && acc.userId !== acc.conversationId) idsToCheck.push(acc.userId);
      for (const id of idsToCheck) {
        if (lastMessageDates[id]) {
          const time = lastMessageDates[id];
          if (time > maxTime) maxTime = time;
        }
      }
    }
    const visitedIndex = recentlyViewedIds.indexOf(contact.id);
    if (visitedIndex >= 0) {
      const visitTime = Date.now() - visitedIndex * 60 * 1000;
      if (visitTime > maxTime) maxTime = visitTime;
    }
    return maxTime;
  };

  // Top 20 recently viewed contacts
  const recentlyViewedContacts = useMemo(() => {
    const result: models.MetaContact[] = [];
    const seenIds = new Set<number>();

    // 1. From recentlyViewedIds localStorage
    for (const id of recentlyViewedIds) {
      const c = contactMap.get(id);
      if (c && !seenIds.has(c.id)) {
        seenIds.add(c.id);
        result.push(c);
      }
    }

    // 2. Complement from conversationHistory if less than 20
    if (result.length < 20) {
      for (let i = conversationHistory.length - 1; i >= 0; i--) {
        const c = conversationHistory[i];
        if (c && contactMap.has(c.id) && !seenIds.has(c.id)) {
          seenIds.add(c.id);
          result.push(contactMap.get(c.id)!);
          if (result.length >= 20) break;
        }
      }
    }

    // 3. Complement from contacts list if still less than 20 (ordered by last message activity)
    if (result.length < 20) {
      const remaining = contacts
        .filter((c) => !seenIds.has(c.id))
        .sort((a, b) => getContactActivityTime(b) - getContactActivityTime(a));
      for (const c of remaining) {
        seenIds.add(c.id);
        result.push(c);
        if (result.length >= 20) break;
      }
    }

    return result.slice(0, 20);
  }, [recentlyViewedIds, contactMap, conversationHistory, contacts, lastMessageDates]);

  // Top 10 recent search contacts
  const recentSearchesContacts = useMemo(() => {
    const result: models.MetaContact[] = [];
    for (const id of recentSearchIds) {
      const c = contactMap.get(id);
      if (c) {
        result.push(c);
      }
    }
    return result.slice(0, 10);
  }, [recentSearchIds, contactMap]);

  const handleClearRecentSearches = () => {
    setRecentSearchIds([]);
    saveStoredIds("loom_recent_searches_ids", []);
  };

  // Calculate relevance score and tier
  const calculateRelevanceScore = (name: string, query: string): { score: number; tier: number } => {
    const lowerName = name.toLowerCase();
    const lowerQuery = query.toLowerCase();

    // Tier 5: Exact match
    if (lowerName === lowerQuery) {
      return { score: 1000, tier: 5 };
    }

    // Tier 4: Starts with query
    if (lowerName.startsWith(lowerQuery)) {
      return { score: 900 - (lowerName.length - lowerQuery.length), tier: 4 };
    }

    // Tier 3: Word boundary match
    const wordBoundaryRegex = new RegExp(`\\b${lowerQuery.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`, 'i');
    if (wordBoundaryRegex.test(lowerName)) {
      const index = lowerName.indexOf(lowerQuery);
      return { score: 800 - index, tier: 3 };
    }

    // Tier 2: Substring match
    if (lowerName.includes(lowerQuery)) {
      const index = lowerName.indexOf(lowerQuery);
      return { score: 700 - index, tier: 2 };
    }

    return { score: 0, tier: 0 };
  };

  // Filter and sort contacts based on search query
  const filteredContacts = useMemo(() => {
    if (!searchQuery.trim()) {
      return [];
    }
    const query = searchQuery.trim();

    const matching = contacts
      .map((contact) => {
        const { score, tier } = calculateRelevanceScore(contact.displayName, query);
        const activityTime = getContactActivityTime(contact);
        return {
          contact,
          score,
          tier,
          activityTime,
        };
      })
      .filter((item) => item.score > 0)
      .sort((a, b) => {
        // 1. Primary: match tier (Exact match > Starts with > Word boundary > Substring > Fuzzy)
        if (b.tier !== a.tier) {
          return b.tier - a.tier;
        }
        // 2. Secondary: last message / conversation activity timestamp (most recent activity first)
        if (b.activityTime !== a.activityTime) {
          return b.activityTime - a.activityTime;
        }
        // 3. Tertiary: detailed relevance score
        if (b.score !== a.score) {
          return b.score - a.score;
        }
        // 4. Fallback: alphabetical order
        return a.contact.displayName.localeCompare(b.contact.displayName);
      })
      .map((item) => item.contact);

    return matching;
  }, [contacts, searchQuery, lastMessageDates, recentlyViewedIds]);
  const displayedContacts = useMemo(
    () => showAllContactResults ? filteredContacts : filteredContacts.slice(0, 5),
    [filteredContacts, showAllContactResults]
  );

  // Scroll horizontal items into view when navigated
  useEffect(() => {
    if (navSection === "recently_viewed" && selectedRecentlyViewedRef.current) {
      selectedRecentlyViewedRef.current.scrollIntoView({
        behavior: "smooth",
        inline: "nearest",
        block: "nearest",
      });
    }
  }, [navSection, recentlyViewedIndex]);

  // Scroll vertical items into view when navigated
  useEffect(() => {
    if (navSection === "recent_searches" && selectedRecentSearchesRef.current) {
      selectedRecentSearchesRef.current.scrollIntoView({
        behavior: "smooth",
        block: "nearest",
      });
    } else if (navSection === "search_results" && selectedSearchResultRef.current) {
      selectedSearchResultRef.current.scrollIntoView({
        behavior: "smooth",
        block: "nearest",
      });
    }
  }, [navSection, recentSearchesIndex, searchResultsIndex]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.preventDefault();
      onOpenChange(false);
      return;
    }

    // Navigation when search query is typed
    if (searchQuery.trim() !== "") {
      const navigationItems = Array.from(
        scrollContainerRef.current?.querySelectorAll<HTMLElement>("[data-search-nav-item]") ?? []
      );
      const focusedIndex = navigationItems.findIndex((item) => item === document.activeElement);
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setNavSection("search_results");
        const nextIndex = Math.min(focusedIndex + 1, navigationItems.length - 1);
        if (nextIndex >= 0) {
          setSearchResultsIndex(nextIndex);
          navigationItems[nextIndex]?.focus();
          navigationItems[nextIndex]?.scrollIntoView({ block: "nearest" });
        }
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        if (focusedIndex > 0) {
          const previousIndex = focusedIndex - 1;
          setSearchResultsIndex(previousIndex);
          navigationItems[previousIndex]?.focus();
          navigationItems[previousIndex]?.scrollIntoView({ block: "nearest" });
        } else {
          setNavSection("input");
          inputRef.current?.focus();
        }
      } else if (e.key === "Enter") {
        e.preventDefault();
        if (focusedIndex >= 0) {
          navigationItems[focusedIndex]?.click();
        } else if (navigationItems.length > 0) {
          navigationItems[0]?.click();
        }
      }
      return;
    }

    // Keyboard navigation for empty search input
    if (navSection === "input") {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        if (recentlyViewedContacts.length > 0) {
          setNavSection("recently_viewed");
          setRecentlyViewedIndex(0);
        } else if (recentSearchesContacts.length > 0) {
          setNavSection("recent_searches");
          setRecentSearchesIndex(0);
        }
      }
    } else if (navSection === "recently_viewed") {
      if (e.key === "ArrowRight") {
        e.preventDefault();
        setRecentlyViewedIndex((prev) =>
          Math.min(prev + 1, recentlyViewedContacts.length - 1)
        );
      } else if (e.key === "ArrowLeft") {
        e.preventDefault();
        setRecentlyViewedIndex((prev) => Math.max(prev - 1, 0));
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        if (recentSearchesContacts.length > 0) {
          setNavSection("recent_searches");
          setRecentSearchesIndex(0);
        }
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setNavSection("input");
        inputRef.current?.focus();
      } else if (e.key === "Enter") {
        e.preventDefault();
        if (recentlyViewedContacts[recentlyViewedIndex]) {
          handleSelectContact(recentlyViewedContacts[recentlyViewedIndex]);
        }
      }
    } else if (navSection === "recent_searches") {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setRecentSearchesIndex((prev) =>
          Math.min(prev + 1, recentSearchesContacts.length - 1)
        );
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        if (recentSearchesIndex > 0) {
          setRecentSearchesIndex((prev) => prev - 1);
        } else {
          if (recentlyViewedContacts.length > 0) {
            setNavSection("recently_viewed");
            setRecentlyViewedIndex(0);
          } else {
            setNavSection("input");
            inputRef.current?.focus();
          }
        }
      } else if (e.key === "Enter") {
        e.preventDefault();
        if (recentSearchesContacts[recentSearchesIndex]) {
          handleSelectContact(recentSearchesContacts[recentSearchesIndex]);
        }
      }
    }
  };

  const handleSelectContact = (contact: models.MetaContact) => {
    // Add to recent search IDs
    const updatedRecent = [
      contact.id,
      ...recentSearchIds.filter((id) => id !== contact.id),
    ].slice(0, 10);
    setRecentSearchIds(updatedRecent);
    saveStoredIds("loom_recent_searches_ids", updatedRecent);

    setSelectedContact(contact);
    onOpenChange(false);
    setSearchQuery("");
    setNavSection("input");
    setRecentlyViewedIndex(0);
    setRecentSearchesIndex(0);
    setSearchResultsIndex(0);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="search-modal__content !top-[10vh] !translate-y-0 max-w-2xl max-h-[80vh] flex flex-col p-0 overflow-hidden"
        onKeyDown={handleKeyDown}
        onFocusCapture={(event) => {
          const navigationItem = (event.target as HTMLElement).closest<HTMLElement>("[data-search-nav-item]");
          if (!navigationItem) return;
          const navigationItems = Array.from(
            scrollContainerRef.current?.querySelectorAll<HTMLElement>("[data-search-nav-item]") ?? []
          );
          setNavSection("search_results");
          setSearchResultsIndex(navigationItems.indexOf(navigationItem));
        }}
      >
        <DialogHeader className="px-6 pt-6 pb-4 border-b">
          <DialogTitle>{t("search_modal_title")}</DialogTitle>
        </DialogHeader>
        <div className="px-6 pt-4 pb-2">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 transform -translate-y-1/2 h-4 w-4 text-muted-foreground" />
            <Input
              ref={inputRef}
              type="text"
              placeholder={t("search_modal_placeholder")}
              value={searchQuery}
              onChange={(e) => {
                const val = e.target.value;
                setSearchQuery(val);
                setShowAllContactResults(false);
                if (val.trim() !== "") {
                  setNavSection("search_results");
                  setSearchResultsIndex(0);
                } else {
                  setNavSection("input");
                }
              }}
              className="pl-10"
              autoCorrect="off"
              autoComplete="off"
              spellCheck="false"
            />
          </div>
        </div>
        <div
          ref={scrollContainerRef}
          className="flex-1 overflow-y-auto px-6 pb-6 min-h-0 space-y-4"
        >
          {!searchQuery.trim() ? (
            /* Initial View: Recently viewed horizontal row + Recent searches block */
            <>
              {/* 1. Recently viewed (Top 20 horizontal row) */}
              {recentlyViewedContacts.length > 0 && (
                <div className="space-y-2">
                  <div className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">
                    {t("recently_viewed")}
                  </div>
                  <div className="flex items-center space-x-3 overflow-x-auto py-2 no-scrollbar px-2">
                    {recentlyViewedContacts.map((contact, index) => {
                      const statusEmojiData = getContactStatusEmoji(contact);
                      const isSelected =
                        navSection === "recently_viewed" &&
                        index === recentlyViewedIndex;
                      return (
                        <div
                          key={contact.id}
                          ref={isSelected ? selectedRecentlyViewedRef : null}
                          className={cn(
                            "flex flex-col items-center flex-shrink-0 w-16 cursor-pointer group text-center p-1.5 rounded-xl transition-all",
                            isSelected
                              ? "ring-2 ring-primary bg-accent/80 scale-105 shadow-sm"
                              : "hover:bg-muted/50"
                          )}
                          onClick={() => handleSelectContact(contact)}
                          title={contact.displayName}
                        >
                          <div className="relative">
                            <Avatar className="h-12 w-12 border border-border/40">
                              <AvatarImage
                                src={contact.avatarUrl}
                                alt={contact.displayName}
                              />
                              <AvatarFallback>
                                {contact.displayName.substring(0, 2).toUpperCase()}
                              </AvatarFallback>
                            </Avatar>
                            {statusEmojiData && (
                              <div
                                className="absolute -top-1 -left-1 bg-background rounded-full p-0.5 border border-border shadow-sm flex items-center justify-center"
                                title={statusEmojiData.emoji}
                              >
                                <Emoji
                                  emoji={statusEmojiData.emoji}
                                  providerInstanceId={statusEmojiData.providerInstanceId}
                                  size={12}
                                />
                              </div>
                            )}
                          </div>
                          <span
                            className={cn(
                              "text-xs font-medium truncate w-full mt-1.5",
                              isSelected
                                ? "text-foreground font-semibold"
                                : "text-muted-foreground group-hover:text-foreground"
                            )}
                          >
                            {contact.displayName}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                </div>
              )}

              {/* 2. Recent searches (Top 10 vertical list) */}
              <div className="space-y-2 pt-2">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">
                    {t("recent_searches")}
                  </span>
                  {recentSearchesContacts.length > 0 && (
                    <button
                      type="button"
                      onClick={handleClearRecentSearches}
                      className="text-xs text-muted-foreground hover:text-foreground hover:underline transition-colors"
                    >
                      {t("clear")}
                    </button>
                  )}
                </div>

                {recentSearchesContacts.length === 0 ? (
                  <div className="py-6 text-center text-xs text-muted-foreground">
                    {t("search_modal_start_typing")}
                  </div>
                ) : (
                  <div className="space-y-1">
                    {recentSearchesContacts.map((contact, index) => {
                      const isSelected =
                        navSection === "recent_searches" &&
                        index === recentSearchesIndex;
                      return (
                        <div
                          key={contact.id}
                          ref={isSelected ? selectedRecentSearchesRef : null}
                          className={cn(
                            "search-modal__result flex items-center space-x-3 p-3 rounded-lg cursor-pointer transition-colors",
                            isSelected
                              ? "bg-accent border border-border shadow-sm"
                              : "hover:bg-muted"
                          )}
                          onClick={() => handleSelectContact(contact)}
                        >
                          <div className="relative">
                            <Avatar>
                              <AvatarImage
                                src={contact.avatarUrl}
                                alt={contact.displayName}
                              />
                              <AvatarFallback>
                                {contact.displayName.substring(0, 2).toUpperCase()}
                              </AvatarFallback>
                            </Avatar>
                            {(() => {
                              const statusEmojiData = getContactStatusEmoji(contact);
                              if (statusEmojiData) {
                                return (
                                  <div
                                    className="absolute -top-1 -left-1 bg-background rounded-full p-0.5 border border-border shadow-sm flex items-center justify-center"
                                    title={statusEmojiData.emoji}
                                  >
                                    <Emoji
                                      emoji={statusEmojiData.emoji}
                                      providerInstanceId={statusEmojiData.providerInstanceId}
                                      size={12}
                                    />
                                  </div>
                                );
                              }
                              return null;
                            })()}
                          </div>
                          <div className="search-modal__contact-info flex flex-col min-w-0 flex-1">
                            <span className="search-modal__contact-name font-medium">
                              {contact.displayName}
                            </span>
                            {contact.linkedAccounts?.length > 0 && (
                              <span className="search-modal__contact-providers flex items-center gap-1 text-xs opacity-40 truncate">
                                {contact.linkedAccounts.map((a, i) => (
                                  <span key={a.providerInstanceId + i} className="search-modal__contact-provider-entry flex items-center gap-0.5">
                                    {providerProtocolById.get(a.providerInstanceId) && (
                                      <ProtocolIcon protocol={providerProtocolById.get(a.providerInstanceId)!} size={12} />
                                    )}
                                    {providerNameById.get(a.providerInstanceId) ?? a.providerInstanceId}
                                    {i < contact.linkedAccounts.length - 1 && <span className="mx-0.5">·</span>}
                                  </span>
                                ))}
                              </span>
                            )}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            </>
          ) : (
            /* Filtered Search Results View */
            contacts.length === 0 ? (
              <div className="py-8 text-center text-muted-foreground">
                {t("loading")}
              </div>
            ) : (
              <div className="space-y-4 pt-1">
                {filteredContacts.length === 0 && searchQuery.trim().length < 3 && (
                  <div className="py-8 text-center text-muted-foreground">
                    {t("search_modal_no_results")}
                  </div>
                )}
                {filteredContacts.length > 0 && (
                  <div className="search-modal__conversation-results space-y-1">
                    <h3 className="search-modal__conversation-results-title pb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                      {t("conversations")}
                    </h3>
                {displayedContacts.map((contact, index) => {
                  const isSelected =
                    navSection === "search_results" && index === searchResultsIndex;
                  return (
                    <div
                      key={contact.id}
                      data-search-nav-item
                      tabIndex={-1}
                      ref={isSelected ? selectedSearchResultRef : null}
                      className={cn(
                        "search-modal__result flex items-center space-x-3 p-3 rounded-lg cursor-pointer transition-colors",
                        isSelected
                          ? "bg-accent border border-border shadow-sm"
                          : "hover:bg-muted"
                      )}
                      onClick={() => handleSelectContact(contact)}
                    >
                      <div className="relative">
                        <Avatar>
                          <AvatarImage src={contact.avatarUrl} alt={contact.displayName} />
                          <AvatarFallback>
                            {contact.displayName.substring(0, 2).toUpperCase()}
                          </AvatarFallback>
                        </Avatar>
                        {(() => {
                          const statusEmojiData = getContactStatusEmoji(contact);
                          if (statusEmojiData) {
                            return (
                              <div
                                className="absolute -top-1 -left-1 bg-background rounded-full p-0.5 border border-border shadow-sm flex items-center justify-center"
                                title={statusEmojiData.emoji}
                              >
                                <Emoji
                                  emoji={statusEmojiData.emoji}
                                  providerInstanceId={statusEmojiData.providerInstanceId}
                                  size={12}
                                />
                              </div>
                            );
                          }
                          return null;
                        })()}
                      </div>
                      <div className="search-modal__contact-info flex flex-col min-w-0 flex-1">
                        <span className="search-modal__contact-name font-medium">
                          {contact.displayName}
                        </span>
                        {contact.linkedAccounts?.length > 0 && (
                          <span className="search-modal__contact-providers flex items-center gap-1 text-xs opacity-40 truncate">
                            {contact.linkedAccounts.map((a, i) => (
                              <span key={a.providerInstanceId + i} className="search-modal__contact-provider-entry flex items-center gap-0.5">
                                {providerProtocolById.get(a.providerInstanceId) && (
                                  <ProtocolIcon protocol={providerProtocolById.get(a.providerInstanceId)!} size={12} />
                                )}
                                {providerNameById.get(a.providerInstanceId) ?? a.providerInstanceId}
                                {i < contact.linkedAccounts.length - 1 && <span className="mx-0.5">·</span>}
                              </span>
                            ))}
                          </span>
                        )}
                      </div>
                    </div>
                  );
                })}
                {filteredContacts.length > 5 && (
                  <button
                    type="button"
                    data-search-nav-item
                    className="search-modal__show-more-contacts w-full rounded-lg border border-transparent px-3 py-2 text-center text-xs font-medium text-primary transition-colors hover:border-border hover:bg-muted focus:border-border focus:bg-muted focus:outline-none focus:ring-2 focus:ring-primary"
                    onClick={() => setShowAllContactResults((showAll) => !showAll)}
                  >
                    {showAllContactResults
                      ? t("show_less")
                      : t("show_more_results", { count: filteredContacts.length - 5 })}
                  </button>
                )}
                  </div>
                )}
                <MessageSearchResults
                  query={searchQuery}
                  onResultSelected={() => onOpenChange(false)}
                />
              </div>
            )
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
