import { create } from "zustand";
import { normalizeReaction } from "./reactionUtils.ts";
import { cleanEmoji } from "./userDisplayNames.ts";

export const REACTION_HALO_DURATION_MS = 5000;
const MAX_PENDING_HALOS = 500;

export interface ReactionHaloActivity {
  id: number;
  conversationId: string;
  messageId: string;
  emoji: string;
}

export function reactionHaloKey(conversationId: string, messageId: string, emoji: string): string {
  // Use the same presentation grouping as the reaction pastilles, including
  // shortcode aliases and their optional skin-tone suffixes.
  const wrapped = `:${emoji.replace(/^:|:$/g, "")}:`;
  return JSON.stringify([conversationId, messageId, normalizeReaction(cleanEmoji(wrapped), false).storedEmoji]);
}

interface ReactionHaloStore {
  pending: Record<string, number>;
  register: (activities: ReactionHaloActivity[]) => void;
  finish: (key: string, activityId: number) => void;
}

// Ephemeral attention survives list acknowledgement and virtualized row
// unmounts. Its lifetime starts only when the pastille is visible and focused.
export const useReactionHaloStore = create<ReactionHaloStore>((set) => ({
  pending: {},
  register: (activities) => set((state) => {
    if (!activities.length) return state;
    const pending = { ...state.pending };
    for (const activity of activities) {
      const key = reactionHaloKey(activity.conversationId, activity.messageId, activity.emoji);
      pending[key] = Math.max(pending[key] ?? 0, activity.id);
    }
    const entries = Object.entries(pending).sort((a, b) => b[1] - a[1]).slice(0, MAX_PENDING_HALOS);
    return { pending: Object.fromEntries(entries) };
  }),
  finish: (key, activityId) => set((state) => {
    // An old timer must never clear a reaction which arrived in the meantime.
    if (state.pending[key] !== activityId) return state;
    const pending = { ...state.pending };
    delete pending[key];
    return { pending };
  }),
}));
