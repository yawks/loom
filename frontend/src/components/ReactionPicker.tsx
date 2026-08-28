import { lazy, Suspense, useEffect, useState } from "react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Button } from "@/components/ui/button";
import { Smile } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import type { EmojiClickData, Theme } from "emoji-picker-react";
import { GetCustomEmojis } from "../../wailsjs/go/main/App";
import { orderCustomEmojis, prepareEmojiSuggestions, recordCustomEmojiUsage, recordStandardEmojiUsage } from "@/lib/emojiUsage";

// Matches the internal CustomEmoji type expected by emoji-picker-react
interface CustomEmoji {
  id: string;
  names: string[];
  imgUrl: string;
}

interface ReactionPickerProps {
  onReactionSelect: (emoji: string) => void;
  currentReactions?: string[];
  className?: string;
  provider?: string;
  instanceId?: string;
  usesNamedReactions?: boolean;
  onOpenChange?: (open: boolean) => void;
}

const EmojiPicker = lazy(() => import("emoji-picker-react"));

export function ReactionPicker({
  onReactionSelect,
  className,
  provider,
  instanceId,
  usesNamedReactions = false,
  onOpenChange,
}: Readonly<ReactionPickerProps>) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [customEmojiCatalog, setCustomEmojiCatalog] = useState<{ instanceId: string; emojis: CustomEmoji[] }>({
    instanceId: "",
    emojis: [],
  });
  const customEmojis = customEmojiCatalog.instanceId === instanceId ? customEmojiCatalog.emojis : [];

  // Fetch custom emojis when the picker opens (once per session)
  useEffect(() => {
    if (!open || !instanceId) return;
    prepareEmojiSuggestions();
    if (customEmojis.length > 0) return;

    GetCustomEmojis(instanceId)
      .then((emojiMap: Record<string, string>) => {
        if (!emojiMap) return;
        const emojis: CustomEmoji[] = Object.entries(emojiMap).map(([name, url]) => ({
          id: name,
          names: [name.replaceAll("_", " ")],
          imgUrl: url as string,
        }));
        setCustomEmojiCatalog({ instanceId, emojis: orderCustomEmojis(instanceId, emojis) });
      })
      .catch(() => {
        // Silently ignore — the picker still shows standard emojis
      });
  }, [open, provider, instanceId, customEmojis.length]);

  const setPickerOpen = (nextOpen: boolean) => {
    setOpen(nextOpen);
    onOpenChange?.(nextOpen);
  };

  const handleEmojiClick = (emojiData: EmojiClickData) => {
    if (emojiData.isCustom) {
      if (instanceId) {
        recordCustomEmojiUsage(instanceId, emojiData.unified);
        setCustomEmojiCatalog((catalog) => ({
          instanceId,
          emojis: orderCustomEmojis(instanceId, catalog.instanceId === instanceId ? catalog.emojis : []),
        }));
      }
      // Custom emoji — pass as :name: so handleReaction can strip the colons
      onReactionSelect(`:${emojiData.unified}:`);
    } else if (usesNamedReactions && emojiData.names[0]) {
      recordStandardEmojiUsage(emojiData.unified, emojiData.unifiedWithoutSkinTone);
      // Some reaction APIs expect a shortcode rather than the Unicode glyph.
      // emoji-picker-react's first name is its canonical shortcode; reversing
      // the alias map could select a loose textual alias such as ":d" instead.
      const shortcode = emojiData.names[0]
        .trim()
        .toLowerCase()
        .replace(/[\s-]+/g, "_");
      onReactionSelect(`:${shortcode}:`);
    } else {
      recordStandardEmojiUsage(emojiData.unified, emojiData.unifiedWithoutSkinTone);
      // Standard unicode emoji
      onReactionSelect(emojiData.emoji);
    }
    setPickerOpen(false);
  };

  const isDark =
    typeof document !== "undefined" &&
    document.documentElement.classList.contains("dark");

  return (
    <Popover open={open} onOpenChange={setPickerOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className={cn("h-6 w-6", className)}
          onClick={(e) => e.stopPropagation()}
          title={t("react")}
        >
          <Smile className="h-4 w-4" />
          <span className="sr-only">{t("react")}</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent
        className="w-auto p-0 border-0 shadow-lg"
        align="start"
        onClick={(e) => e.stopPropagation()}
        onOpenAutoFocus={(e) => e.preventDefault()}
        onCloseAutoFocus={(e) => e.preventDefault()}
      >
        <Suspense fallback={<div className="h-[400px] w-[352px]" />}>
          <EmojiPicker
            onEmojiClick={handleEmojiClick}
            customEmojis={customEmojis}
            height={400}
            searchPlaceholder={t("search_emoji") ?? "Search emojis…"}
            theme={(isDark ? "dark" : "light") as Theme}
            lazyLoadEmojis
          />
        </Suspense>
      </PopoverContent>
    </Popover>
  );
}
