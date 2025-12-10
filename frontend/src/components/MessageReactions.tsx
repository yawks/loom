import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cleanSlackEmoji, getUserDisplayName } from "@/lib/userDisplayNames";
import { useMemo, useState } from "react";

import { SlackEmoji } from "./SlackEmoji";
import { cn } from "@/lib/utils";
import type { models } from "../../wailsjs/go/models";

interface MessageReactionsProps {
  reactions: models.Reaction[];
  participantNames?: Map<string, string>;
  currentUserId?: string;
  onReactionClick?: (emoji: string) => void;
  className?: string;
  providerInstanceId?: string; // For Slack custom emojis
  isSlack?: boolean; // Whether this is a Slack conversation
  allMessages?: models.Message[]; // All messages to extract sender names
}

interface ReactionGroup {
  emoji: string;
  count: number;
  userIds: string[];
}

export function MessageReactions({
  reactions,
  participantNames,
  currentUserId,
  onReactionClick,
  className,
  providerInstanceId,
  isSlack = false,
  allMessages,
}: MessageReactionsProps) {
  // Group reactions by emoji (cleaning Slack emojis to remove skin-tone modifiers)
  const reactionGroups = useMemo(() => {
    const groups = new Map<string, ReactionGroup>();
    
    reactions.forEach((reaction) => {
      // Clean Slack emoji to remove skin-tone modifiers (e.g., ":santa::skin-tone-2:" -> ":santa:")
      // Always clean for Slack, even if some reactions might already be cleaned in DB
      const cleanedEmoji = isSlack ? cleanSlackEmoji(reaction.emoji) : reaction.emoji;
      
      const existing = groups.get(cleanedEmoji);
      if (existing) {
        existing.count++;
        if (!existing.userIds.includes(reaction.userId)) {
          existing.userIds.push(reaction.userId);
        }
      } else {
        groups.set(cleanedEmoji, {
          emoji: cleanedEmoji,
          count: 1,
          userIds: [reaction.userId],
        });
      }
    });

    return Array.from(groups.values());
  }, [reactions, isSlack]);

  if (reactionGroups.length === 0) {
    return null;
  }

  return (
    <div className={cn("flex flex-wrap gap-1 mt-1", className)}>
      {reactionGroups.map((group) => {
        const hasCurrentUser = currentUserId && group.userIds.includes(currentUserId);
        // Always try to get display names, not just for groups
        const displayNames = group.userIds
          .map((userId) => getUserDisplayName(userId, { participantNames, allMessages }))
          .filter(Boolean);

        // Format emoji for SlackEmoji (needs colons around the name)
        const formattedEmoji = isSlack && !group.emoji.startsWith(":") 
          ? `:${group.emoji}:` 
          : group.emoji;
        
        const buttonContent = (
          <>
            {isSlack && providerInstanceId ? (
              <SlackEmoji
                emoji={formattedEmoji}
                providerInstanceId={providerInstanceId}
                size={16}
                className="inline-block align-baseline"
              />
            ) : (
              <span>{group.emoji}</span>
            )}
            {group.userIds.length > 1 && <span className="ml-0.5">{group.userIds.length}</span>}
          </>
        );

        const button = (
          <button
            onClick={() => onReactionClick?.(group.emoji)}
            className={cn(
              "inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs border-2 transition-colors",
              hasCurrentUser
                ? "bg-primary/30 border-primary text-primary font-medium"
                : "bg-muted border-border text-foreground hover:bg-muted/80"
            )}
          >
            {buttonContent}
          </button>
        );

        // Show popover if we have display names (for groups or if we have participant names)
        if (displayNames.length > 0) {
          return (
            <ReactionPopover key={group.emoji} button={button}>
              <div className="flex flex-col gap-1">
                {displayNames.map((name, idx) => (
                  <span key={idx} className="text-sm">{name}</span>
                ))}
              </div>
            </ReactionPopover>
          );
        }

        return <div key={group.emoji}>{button}</div>;
      })}
    </div>
  );
}

// Helper component to handle hover-triggered popover
function ReactionPopover({ button, children }: { button: React.ReactNode; children: React.ReactNode }) {
  const [open, setOpen] = useState(false);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <div
          onMouseEnter={() => setOpen(true)}
          onMouseLeave={() => setOpen(false)}
        >
          {button}
        </div>
      </PopoverTrigger>
      <PopoverContent 
        className="w-auto p-2" 
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => setOpen(false)}
        onOpenAutoFocus={(e) => e.preventDefault()}
      >
        {children}
      </PopoverContent>
    </Popover>
  );
}

