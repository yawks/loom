import { useState } from "react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Button } from "@/components/ui/button";
import { Smile } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";

// Common emoji reactions (WhatsApp supports these)
const COMMON_REACTIONS = ["👍", "❤️", "😂", "😮", "😢", "🙏"];

interface ReactionPickerProps {
  onReactionSelect: (emoji: string) => void;
  currentReactions?: string[]; // Emojis that the current user has already reacted with
  className?: string;
}

export function ReactionPicker({
  onReactionSelect,
  currentReactions = [],
  className,
}: ReactionPickerProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);

  const handleReactionClick = (emoji: string) => {
    onReactionSelect(emoji);
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
          {COMMON_REACTIONS.map((emoji) => {
            const isActive = currentReactions.includes(emoji);
            return (
              <button
                key={emoji}
                onClick={() => handleReactionClick(emoji)}
                className={cn(
                  "text-lg px-2 py-1 rounded hover:bg-muted transition-colors",
                  isActive && "bg-primary/20"
                )}
                title={emoji}
              >
                {emoji}
              </button>
            );
          })}
        </div>
      </PopoverContent>
    </Popover>
  );
}


