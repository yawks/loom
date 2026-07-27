import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { useMemo, useState, useRef, useEffect } from "react";

import { Emoji } from "./Emoji";
import { cleanEmoji } from "@/lib/userDisplayNames";
import { cn } from "@/lib/utils";
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

    // If not found and ID contains ":", try without the ":digits" part (for WhatsApp LID format)
    // e.g., "33662865152:47@s.whatsapp.net" -> "33662865152@s.whatsapp.net"
    if (userId.includes(":") && (userId.includes("@s.whatsapp.net") || userId.includes("@g.us"))) {
      const normalizedId = userId.replace(/:\d+@/, "@");
      const normalizedName = participantNames.get(normalizedId);
      if (normalizedName && normalizedName.trim().length > 0) {
        return normalizedName;
      }
    }
  }

  // If not found in participantNames, try to find in messages (for provider user IDs)
  if (allMessages) {
    for (const message of allMessages) {
      if (message.senderId === userId && message.senderName && message.senderName.trim().length > 0) {
        return message.senderName;
      }
    }
  }

  // For provider user IDs (U1234567890), return a formatted version
  if (userId.startsWith("U") && /^U[A-Z0-9]+$/.test(userId)) {
    // provider user ID - return as-is (will be handled by UI or backend lookup)
    return userId;
  }

  // Robust handling: extract local part from various WhatsApp ID formats
  // Supports: "33603018166@s.whatsapp.net", "33662865152:47@s.whatsapp.net" (LID format)
  let phoneNumber: string | null = null;

  // Match "digits" optionally followed by ":digits@server"
  const match = userId.match(/^(\d+)(?::\d+)?@/);
  if (match) {
    phoneNumber = match[1];
  }

  if (phoneNumber) {
    // If this looks like a French number (starts with 33 and 11 digits) format nicely
    if (phoneNumber.startsWith("33") && phoneNumber.length === 11) {
      const countryCode = phoneNumber.substring(0, 2); // "33"
      const rest = phoneNumber.substring(2); // 9 digits
      const formatted = `+${countryCode} ${rest.substring(0, 1)} ${rest.substring(1, 3)} ${rest.substring(3, 5)} ${rest.substring(5, 7)} ${rest.substring(7, 9)}`;
      return formatted;
    }
    // For other numeric local parts, return with a leading + and no odd grouping
    return `+${phoneNumber}`;
  }

  // Fallback for other ID formats: try to return a readable label
  return userId
    .replace(/^user-/, "")
    .replace(/^whatsapp-/, "")
    .replace(/^[a-z]+-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

interface MessageReactionsProps {
  reactions: models.Reaction[];
  isGroup: boolean;
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
  isGroup,
  participantNames,
  currentUserId,
  providerInstanceId,
  allMessages,
  onReactionClick,
  className,
}: MessageReactionsProps) {
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

      // Group by cleaned emoji
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
  }, [reactions]);

  const rootRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const firstBtn = rootRef.current?.querySelector("button");
    if (firstBtn) {
      const r = firstBtn.getBoundingClientRect();
      const s = getComputedStyle(firstBtn);
      console.log("[Reactions] button height:", r.height, "px | paddingTop:", s.paddingTop, "paddingBottom:", s.paddingBottom, "fontSize:", s.fontSize, "lineHeight:", s.lineHeight);
    }
  }, []);

  if (reactionGroups.length === 0) {
    return null;
  }

  return (
    <div ref={rootRef} className={cn("flex flex-wrap gap-1 items-center mt-1", className)}>
      {reactionGroups.map((group) => {
        const hasCurrentUser = currentUserId && group.userIds.includes(currentUserId);
        const displayNames = isGroup
          ? group.userIds
              .map((userId) => getDisplayName(userId, participantNames, allMessages))
              .filter(Boolean)
          : [];

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

        if (isGroup && displayNames.length > 0) {
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

