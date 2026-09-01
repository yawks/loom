import { useEffect, useRef, useState } from "react";
import { Search } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { MessageSearchResults } from "./MessageSearchResults";

interface ConversationSearchModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  metaContactId: number;
  displayName: string;
  avatarUrl?: string;
}

export function ConversationSearchModal({
  open,
  onOpenChange,
  metaContactId,
  displayName,
  avatarUrl,
}: ConversationSearchModalProps) {
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  const [submittedQuery, setSubmittedQuery] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  const resultsRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const timer = window.setTimeout(() => inputRef.current?.focus(), 100);
    return () => window.clearTimeout(timer);
  }, [open]);

  const handleOpenChange = (nextOpen: boolean) => {
    if (!nextOpen) {
      setQuery("");
      setSubmittedQuery("");
    }
    onOpenChange(nextOpen);
  };

  const submitSearch = () => {
    const trimmed = query.trim();
    if (trimmed.length >= 3) setSubmittedQuery(trimmed);
  };

  const handleKeyDown = (event: React.KeyboardEvent) => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp" && event.key !== "Enter") return;
    const navigationItems = Array.from(
      resultsRef.current?.querySelectorAll<HTMLElement>("[data-search-nav-item]") ?? []
    );
    const focusedIndex = navigationItems.findIndex((item) => item === document.activeElement);

    if (event.key === "Enter") {
      if (focusedIndex >= 0) {
        event.preventDefault();
        navigationItems[focusedIndex]?.click();
      }
      return;
    }

    if (navigationItems.length === 0) return;
    event.preventDefault();
    if (document.activeElement === inputRef.current) {
      const targetIndex = event.key === "ArrowDown" ? 0 : navigationItems.length - 1;
      navigationItems[targetIndex]?.focus();
      navigationItems[targetIndex]?.scrollIntoView({ block: "nearest" });
      return;
    }

    const targetIndex = event.key === "ArrowDown" ? focusedIndex + 1 : focusedIndex - 1;
    if (focusedIndex < 0 || targetIndex < 0 || targetIndex >= navigationItems.length) {
      inputRef.current?.focus();
      return;
    }
    navigationItems[targetIndex]?.focus();
    navigationItems[targetIndex]?.scrollIntoView({ block: "nearest" });
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        className="conversation-search-modal__content !top-[10vh] !translate-y-0 max-w-2xl max-h-[80vh] flex flex-col p-0 overflow-hidden"
        onKeyDown={handleKeyDown}
      >
        <DialogHeader className="border-b px-6 pb-4 pt-6">
          <DialogTitle className="flex items-center gap-3">
            <Avatar className="h-9 w-9">
              <AvatarImage src={avatarUrl} alt={displayName} />
              <AvatarFallback>{displayName.substring(0, 2).toUpperCase()}</AvatarFallback>
            </Avatar>
            <span className="truncate">{t("search_in_conversation_title", { name: displayName })}</span>
          </DialogTitle>
        </DialogHeader>
        <form
          className="flex gap-2 px-6 pb-2 pt-4"
          onSubmit={(event) => {
            event.preventDefault();
            submitSearch();
          }}
        >
          <div className="relative flex-1">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              ref={inputRef}
              value={query}
              onChange={(event) => {
                setQuery(event.target.value);
                setSubmittedQuery("");
              }}
              placeholder={t("search_in_conversation_placeholder")}
              className="pl-10"
              autoCorrect="off"
              autoComplete="off"
              spellCheck="false"
            />
          </div>
          <Button type="submit" disabled={query.trim().length < 3}>
            {t("ok")}
          </Button>
        </form>
        <div ref={resultsRef} className="min-h-0 flex-1 overflow-y-auto px-6 pb-6">
          {submittedQuery ? (
            <MessageSearchResults
              query={submittedQuery}
              metaContactId={metaContactId}
              debounceMs={0}
              onResultSelected={() => onOpenChange(false)}
            />
          ) : (
            <div className="py-8 text-center text-sm text-muted-foreground">
              {t("search_modal_start_typing")}
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
