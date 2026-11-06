import { useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { GetReactionActivitySummaries, MarkReactionActivitiesSeen } from "../../wailsjs/go/main/App";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { useAppStore } from "@/lib/store";
import { canReadVisibleMessages } from "@/lib/windowFocus";
import { useReactionHaloStore } from "@/lib/reactionHaloStore";

export function useReactionActivities() {
  const query = useQuery({
    queryKey: ["reactionActivities"],
    queryFn: GetReactionActivitySummaries,
    staleTime: 5000,
  });
  return query.data ?? {};
}

// Mounted once by the conversation list; other badge consumers share the query.
export function useTrackReactionActivities() {
  const queryClient = useQueryClient();
  const selectedContact = useAppStore((state) => state.selectedContact);
  const activities = useReactionActivities();

  useEffect(() => {
    // Collapse sync bursts into one bounded summary query.
    let timer: ReturnType<typeof setTimeout> | undefined;
    const conversations = new Set<string>();
    const scheduleRefresh = () => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => {
        void queryClient.invalidateQueries({ queryKey: ["reactionActivities"] });
        void queryClient.invalidateQueries({ queryKey: ["allLastMessageTimestamps"] });
        // A recovered batch is already persisted. Reload its final state instead
        // of replaying old adds/removals against the renderer in arrival order.
        for (const conversationId of conversations) {
          void queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
          void queryClient.invalidateQueries({ queryKey: ["threads", conversationId] });
          void queryClient.invalidateQueries({ queryKey: ["contact-exchange-stats", conversationId] });
        }
        conversations.clear();
      }, 100);
    };
    const unsubscribe = EventsOn("reaction", scheduleRefresh);
    const unsubscribeBatch = EventsOn("reactions-batch", (payload: string) => {
      try {
        const event = JSON.parse(payload) as { conversationId?: string };
        if (event.conversationId) conversations.add(event.conversationId);
        scheduleRefresh();
      } catch (error) {
        console.error("Failed to parse recovered reaction batch:", error);
      }
    });
    return () => {
      if (timer) clearTimeout(timer);
      unsubscribe();
      unsubscribeBatch();
    };
  }, [queryClient]);

  useEffect(() => {
    const conversationId = selectedContact?.linkedAccounts[0]?.conversationId;
    const summary = conversationId ? activities[conversationId] : undefined;
    if (!conversationId || !summary?.unreadCount) return;
    let pending = false;
    const acknowledge = () => {
      if (pending || !canReadVisibleMessages()) return;
      pending = true;
      void MarkReactionActivitiesSeen(conversationId, summary.throughId)
        .then((activities) => {
          useReactionHaloStore.getState().register(activities ?? []);
          return queryClient.invalidateQueries({ queryKey: ["reactionActivities"] });
        })
        .catch((error) => console.error("Failed to acknowledge reaction activity:", error))
        .finally(() => { pending = false; });
    };
    acknowledge();
    window.addEventListener("focus", acknowledge);
    document.addEventListener("visibilitychange", acknowledge);
    return () => {
      window.removeEventListener("focus", acknowledge);
      document.removeEventListener("visibilitychange", acknowledge);
    };
  }, [selectedContact, activities, queryClient]);
}
