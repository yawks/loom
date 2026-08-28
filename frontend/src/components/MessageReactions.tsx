import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { useMemo, useState, useRef } from "react";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { useTranslation } from "react-i18next";

import { Emoji } from "./Emoji";
import { cleanEmoji } from "@/lib/userDisplayNames";
import { normalizeReaction } from "@/lib/reactionUtils";
import { cn } from "@/lib/utils";
import { canonicalUserId, sameUserId } from "@/lib/userIdentity";
import type { models } from "../../wailsjs/go/models";

// Get display name for a user ID (same logic as in ConversationDetailsView)
function getDisplayName(
  userId: string,
  participantNames?: Map<string, string>,
  allMessages?: models.Message[]
): string {
  // First try to get from participantNames with the exact ID
  if (participantNames) {
    const name = participantNames.get(userId);
    if (name && name.trim().length > 0) {
      return name;
    }

    const canonicalId = canonicalUserId(userId);
    for (const [participantId, participantName] of participantNames) {
      if (canonicalUserId(participantId) === canonicalId && participantName.trim().length > 0) {
        return participantName;
      }
    }

  }

  // If not found in participantNames, try to find in messages (for provider user IDs)
  if (allMessages) {
    for (const message of allMessages) {
      if (sameUserId(message.senderId, userId) && message.senderName && message.senderName.trim().length > 0) {
        return message.senderName;
      }
    }
  }

  // For provider user IDs (U1234567890), return a formatted version
  if (userId.startsWith("U") && /^U[A-Z0-9]+$/.test(userId)) {
    // provider user ID - return as-is (will be handled by UI or backend lookup)
    return userId;
  }

  // Fallback for other ID formats: try to return a readable label
  return userId
    .replace(/^user-/, "")
    .replace(/^[a-z]+-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

function getUserAvatarUrl(userId: string, allMessages?: models.Message[]): string | undefined {
  if (allMessages) {
    for (const message of allMessages) {
      if (sameUserId(message.senderId, userId) && message.senderAvatarUrl) {
        return message.senderAvatarUrl;
      }
    }
  }
  return undefined;
}

interface MessageReactionsProps {
  reactions: models.Reaction[];
  isGroup?: boolean;
  participantNames?: Map<string, string>;
  currentUserId?: string;
  providerInstanceId?: string;
  allMessages?: models.Message[]; // All messages in the conversation (for user name lookup)
  onReactionClick?: (emoji: string) => void;
  className?: string;
}

interface ReactionGroup {
  emoji: string;
  count: number;
  userIds: string[];
}

export function MessageReactions({
  reactions,
  isGroup: _isGroup,
  participantNames,
  currentUserId,
  providerInstanceId,
  allMessages,
  onReactionClick,
  className,
}: MessageReactionsProps) {
  const { t } = useTranslation();

  // Group reactions by emoji (after cleaning skin-tones)
  const reactionGroups = useMemo(() => {
    const groups = new Map<string, ReactionGroup>();

    reactions.forEach((reaction) => {
      // First, normalize format: ensure emoji has colons for proper cleaning
      // Reactions can be stored as "+1::skin-tone-2" or ":+1::skin-tone-2:"
      let normalizedEmoji = reaction.emoji;
      if (!normalizedEmoji.startsWith(":")) {
        normalizedEmoji = `:${normalizedEmoji}`;
      }
      if (!normalizedEmoji.endsWith(":")) {
        normalizedEmoji = `${normalizedEmoji}:`;
      }

      // Now clean skin-tone modifiers from normalized emoji
      let cleanedEmoji = cleanEmoji(normalizedEmoji);

      // Ensure cleaned emoji still has colons (in case it was removed)
      if (!cleanedEmoji.startsWith(":")) {
        cleanedEmoji = `:${cleanedEmoji}`;
      }
      if (!cleanedEmoji.endsWith(":")) {
        cleanedEmoji = `${cleanedEmoji}:`;
      }

      // Aliases and Unicode glyphs can represent the same reaction depending
      // on whether this row came from the optimistic update or the provider
      // echo. Always group them under one canonical key.
      const canonicalEmoji = normalizeReaction(cleanedEmoji, false).storedEmoji;
      const existing = groups.get(canonicalEmoji);
      if (existing) {
        if (!existing.userIds.some((userId) => sameUserId(userId, reaction.userId))) {
          existing.userIds.push(reaction.userId);
          existing.count++;
        }
      } else {
        groups.set(canonicalEmoji, {
          emoji: canonicalEmoji,
          count: 1,
          userIds: [reaction.userId],
        });
      }
    });

    return Array.from(groups.values());
  }, [reactions]);

  const rootRef = useRef<HTMLDivElement>(null);

  if (reactionGroups.length === 0) {
    return null;
  }

  return (
    <div ref={rootRef} className={cn("flex flex-wrap gap-1 items-center mt-1", className)}>
      {reactionGroups.map((group) => {
        const hasCurrentUser = group.userIds.some((userId) => sameUserId(userId, currentUserId));

        const buttonContent = (
          <>
            <Emoji
              emoji={group.emoji}
              providerInstanceId={providerInstanceId}
              size={14}
              className="inline align-middle"
            />
            {group.userIds.length > 1 && <span className="ml-0.5">{group.userIds.length}</span>}
          </>
        );

        const button = (
          <button
            onClick={() => onReactionClick?.(group.emoji)}
            className={cn(
              "inline-flex items-center gap-1 px-2 h-[1.375rem] rounded-md text-[0.55rem] transition-colors",
              hasCurrentUser
                ? "bg-primary/20 border-primary/50 text-primary"
                : "bg-muted border-border text-foreground hover:bg-muted/80"
            )}
          >
            {buttonContent}
          </button>
        );

        if (group.userIds.length > 0) {
          return (
            <ReactionPopover key={group.emoji} button={button}>
              <div className="flex flex-col gap-1.5 p-1 min-w-[100px]">
                {group.userIds.map((userId) => {
                  const isMe = sameUserId(userId, currentUserId);
                  const name = isMe
                    ? t("you") || "Vous"
                    : getDisplayName(userId, participantNames, allMessages);
                  const avatarUrl = getUserAvatarUrl(userId, allMessages);
                  const fallbackInitials = name ? name.substring(0, 2).toUpperCase() : "?";

                  return (
                    <div key={userId} className="flex items-center gap-2 text-xs">
                      <Avatar className="h-5 w-5 shrink-0">
                        <AvatarImage src={avatarUrl} />
                        <AvatarFallback className="text-[10px]">{fallbackInitials}</AvatarFallback>
                      </Avatar>
                      <span className="font-medium text-popover-foreground whitespace-nowrap">{name}</span>
                    </div>
                  );
                })}
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
        className="w-auto p-2 border shadow-md rounded-md bg-popover text-popover-foreground"
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => setOpen(false)}
        onOpenAutoFocus={(e) => e.preventDefault()}
      >
        {children}
      </PopoverContent>
    </Popover>
  );
}
