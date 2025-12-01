import { Edit, MoreVertical, Trash2 } from "lucide-react";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { ReactionPicker } from "./ReactionPicker";

interface MessageActionsProps {
  isFromMe: boolean;
  hasAttachments: boolean;
  onEdit: () => void;
  onDelete: () => void;
  onReact?: (emoji: string) => void;
  currentReactions?: string[];
  className?: string;
  messageId?: string;
  openActionsMessageId?: string | null;
}

export function MessageActions({
  isFromMe,
  hasAttachments,
  onEdit,
  onDelete,
  onReact,
  currentReactions = [],
  className,
  messageId,
  openActionsMessageId,
}: MessageActionsProps) {
  const { t } = useTranslation();
  const [internalOpen, setInternalOpen] = useState(false);

  // Only allow popover to be open if this message is being hovered
  const canBeOpen = !messageId || openActionsMessageId === messageId;
  // Close popover automatically when message loses focus
  const open = internalOpen && canBeOpen;
  
  // Close popover when message loses focus
  // Use a ref to track if we should close to avoid setState in effect warning
  const shouldCloseRef = useRef(false);
  useEffect(() => {
    if (!canBeOpen && internalOpen) {
      shouldCloseRef.current = true;
      // Use requestAnimationFrame to defer state update
      requestAnimationFrame(() => {
        if (shouldCloseRef.current) {
          setInternalOpen(false);
          shouldCloseRef.current = false;
        }
      });
    }
  }, [canBeOpen, internalOpen]);

  return (
    <div className={cn("flex items-center gap-1", className)}>
      {onReact && (
        <ReactionPicker
          onReactionSelect={onReact}
          currentReactions={currentReactions}
          className="opacity-0 group-hover:opacity-100 transition-opacity"
        />
      )}
      {isFromMe && (
        <Popover open={open} onOpenChange={setInternalOpen}>
          <PopoverTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className={cn(
                "h-6 w-6 opacity-0 group-hover:opacity-100 transition-opacity border bg-background",
                className
              )}
              onClick={(e) => e.stopPropagation()}
            >
              <MoreVertical className="h-4 w-4" />
              <span className="sr-only">{t("message_actions")}</span>
            </Button>
          </PopoverTrigger>
          <PopoverContent className="w-40 p-1 border" align="end">
            <div className="flex flex-col">
              {!hasAttachments && (
                <Button
                  variant="ghost"
                  className="justify-start gap-2 h-9"
                  onClick={(e) => {
                    e.stopPropagation();
                    onEdit();
                  }}
                >
                  <Edit className="h-4 w-4" />
                  {t("edit_message")}
                </Button>
              )}
              <Button
                variant="ghost"
                className="justify-start gap-2 h-9 text-destructive hover:text-destructive hover:bg-destructive/10"
                onClick={(e) => {
                  e.stopPropagation();
                  onDelete();
                }}
              >
                <Trash2 className="h-4 w-4" />
                {t("delete_message")}
              </Button>
            </div>
          </PopoverContent>
        </Popover>
      )}
    </div>
  );
}

