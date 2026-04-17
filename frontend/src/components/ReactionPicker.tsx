import { useEffect, useState } from "react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Button } from "@/components/ui/button";
import { Smile } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import EmojiPicker, { type EmojiClickData, Theme } from "emoji-picker-react";
import { GetCustomEmojis } from "../../wailsjs/go/main/App";

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
}

export function ReactionPicker({
  onReactionSelect,
  className,
  provider,
  instanceId,
}: Readonly<ReactionPickerProps>) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [customEmojis, setCustomEmojis] = useState<CustomEmoji[]>([]);

  // Fetch custom emojis when the picker opens (once per session)
  useEffect(() => {
    if (!open || !instanceId) return;
    if (customEmojis.length > 0) return;

    GetCustomEmojis(instanceId)
      .then((emojiMap: Record<string, string>) => {
        if (!emojiMap) return;
        const emojis: CustomEmoji[] = Object.entries(emojiMap).map(([name, url]) => ({
          id: name,
          names: [name.replaceAll("_", " ")],
          imgUrl: url as string,
        }));
        setCustomEmojis(emojis);
      })
      .catch(() => {
        // Silently ignore — the picker still shows standard emojis
      });
  }, [open, provider, instanceId, customEmojis.length]);

  const handleEmojiClick = (emojiData: EmojiClickData) => {
    if (emojiData.isCustom) {
      // Custom emoji — pass as :name: so handleReaction can strip the colons
      onReactionSelect(`:${emojiData.unified}:`);
    } else {
      // Standard unicode emoji
      onReactionSelect(emojiData.emoji);
    }
    setOpen(false);
  };

  const isDark =
    typeof document !== "undefined" &&
    document.documentElement.classList.contains("dark");

  return (
    <Popover open={open} onOpenChange={setOpen}>
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
      >
        <EmojiPicker
          onEmojiClick={handleEmojiClick}
          customEmojis={customEmojis}
          height={400}
          searchPlaceholder={t("search_emoji") ?? "Search emojis…"}
          theme={isDark ? Theme.DARK : Theme.LIGHT}
          lazyLoadEmojis
        />
      </PopoverContent>
    </Popover>
  );
}
