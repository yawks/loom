import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";

import { Button } from "@/components/ui/button";
import { Smile } from "lucide-react";
import { cn } from "@/lib/utils";
import { useState } from "react";
import { useTranslation } from "react-i18next";

// Common emoji reactions with their Slack codes
// For WhatsApp, we use Unicode emojis. For Slack, we need to convert to Slack codes.
const COMMON_REACTIONS = [
  { unicode: "👍", slack: "+1", label: "Thumbs up" },
  { unicode: "❤️", slack: "heart", label: "Heart" },
  { unicode: "😂", slack: "joy", label: "Joy" },
  { unicode: "😮", slack: "open_mouth", label: "Surprised" },
  { unicode: "😢", slack: "cry", label: "Cry" },
  { unicode: "🙏", slack: "pray", label: "Pray" },
];

interface ReactionPickerProps {
  onReactionSelect: (emoji: string) => void;
  currentReactions?: string[]; // Emojis that the current user has already reacted with
  className?: string;
  isSlack?: boolean; // Whether this is a Slack conversation
}

export function ReactionPicker({
  onReactionSelect,
  currentReactions = [],
  className,
  isSlack = false,
}: ReactionPickerProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);

  const handleReactionClick = (reaction: typeof COMMON_REACTIONS[0]) => {
    // For Slack, use the Slack code; for WhatsApp, use Unicode emoji
    const emojiToSend = isSlack ? reaction.slack : reaction.unicode;
    onReactionSelect(emojiToSend);
    setOpen(false);
  };

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
      <PopoverContent className="w-auto p-2" align="start" onClick={(e) => e.stopPropagation()}>
        <div className="flex gap-1">
          {COMMON_REACTIONS.map((reaction) => {
            // For checking if active, compare with the format we're using
            const emojiToCheck = isSlack ? reaction.slack : reaction.unicode;
            const isActive = currentReactions.includes(emojiToCheck);
            return (
              <button
                key={reaction.unicode}
                onClick={() => handleReactionClick(reaction)}
                className={cn(
                  "text-lg px-2 py-1 rounded hover:bg-muted transition-colors",
                  isActive && "bg-primary/20"
                )}
                title={reaction.label}
              >
                {reaction.unicode}
              </button>
            );
          })}
        </div>
      </PopoverContent>
    </Popover>
  );
}





