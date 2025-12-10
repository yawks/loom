import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useEffect, useMemo, useRef, useState } from "react";

import { Input } from "@/components/ui/input";
import { Search } from "lucide-react";
import { SlackEmoji } from "./SlackEmoji";
import { cn } from "@/lib/utils";
import { getContactStatusEmoji } from "@/lib/statusEmoji";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";

interface SearchModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function SearchModal({ open, onOpenChange }: SearchModalProps) {
  const { t } = useTranslation();
  const [searchQuery, setSearchQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const selectedItemRef = useRef<HTMLDivElement>(null);
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const setSelectedContact = useAppStore((state) => state.setSelectedContact);
  const contacts = useAppStore((state) => state.metaContacts);

  // Filter contacts based on search query
  const filteredContacts = useMemo(() => {
    if (!searchQuery.trim()) {
      return contacts;
    }
    const query = searchQuery.toLowerCase().trim();
    return contacts.filter((contact) =>
      contact.displayName.toLowerCase().includes(query)
    );
  }, [contacts, searchQuery]);

  // Focus input when modal opens
  useEffect(() => {
    if (open) {
      // Small delay to ensure the dialog is fully rendered
      setTimeout(() => {
        inputRef.current?.focus();
        setSearchQuery("");
        setSelectedIndex(0);
      }, 100);
    }
  }, [open]);

  // Scroll selected item into view
  useEffect(() => {
    if (selectedItemRef.current) {
      selectedItemRef.current.scrollIntoView({
        behavior: "smooth",
        block: "nearest",
      });
    }
  }, [selectedIndex]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelectedIndex((prev) =>
        prev < filteredContacts.length - 1 ? prev + 1 : prev
      );
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelectedIndex((prev) => (prev > 0 ? prev - 1 : 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (filteredContacts[selectedIndex]) {
        handleSelectContact(filteredContacts[selectedIndex]);
      }
    } else if (e.key === "Escape") {
      e.preventDefault();
      onOpenChange(false);
    }
  };

  const handleSelectContact = (contact: models.MetaContact) => {
    setSelectedContact(contact);
    onOpenChange(false);
    setSearchQuery("");
    setSelectedIndex(0);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl max-h-[80vh] flex flex-col p-0">
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
                setSearchQuery(e.target.value);
                setSelectedIndex(0);
              }}
              onKeyDown={handleKeyDown}
              autoCorrect="off"
              autoCapitalize="none"
              spellCheck={false}
              className="pl-10"
            />
          </div>
        </div>
        <div
          ref={scrollContainerRef}
          className="flex-1 overflow-y-auto px-6 pb-6 min-h-0"
        >
          {contacts.length === 0 ? (
            <div className="py-8 text-center text-muted-foreground">
              {t("loading")}
            </div>
          ) : filteredContacts.length === 0 ? (
            <div className="py-8 text-center text-muted-foreground">
              {searchQuery.trim()
                ? t("search_modal_no_results")
                : t("search_modal_start_typing")}
            </div>
          ) : (
            <div className="space-y-1">
              {filteredContacts.map((contact, index) => (
                <div
                  key={contact.id}
                  ref={index === selectedIndex ? selectedItemRef : null}
                  className={cn(
                    "flex items-center space-x-3 p-3 rounded-lg cursor-pointer transition-colors",
                    index === selectedIndex
                      ? "bg-accent border border-border"
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
                    {/* Status emoji overlay */}
                    {(() => {
                      const statusEmojiData = getContactStatusEmoji(contact);
                      if (statusEmojiData) {
                        return (
                          <div
                            className="absolute -top-1 -left-1 bg-background rounded-full p-0.5 border border-border shadow-sm flex items-center justify-center"
                            title={statusEmojiData.emoji}
                          >
                            <SlackEmoji
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
                  <span className="font-medium">{contact.displayName}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

