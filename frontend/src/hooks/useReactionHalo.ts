import { useEffect, useRef } from "react";
import { reactionHaloKey, REACTION_HALO_DURATION_MS, useReactionHaloStore } from "@/lib/reactionHaloStore";
import { canReadVisibleMessages } from "@/lib/windowFocus";

export function useReactionHalo(conversationId: string, messageId: string, emoji: string) {
  const ref = useRef<HTMLButtonElement>(null);
  const key = reactionHaloKey(conversationId, messageId, emoji);
  const activityId = useReactionHaloStore((state) => state.pending[key]);

  useEffect(() => {
    const element = ref.current;
    if (!element || !activityId) return;
    let visible = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const update = () => {
      if (timer) clearTimeout(timer);
      timer = undefined;
      if (visible && canReadVisibleMessages()) {
        timer = setTimeout(() => useReactionHaloStore.getState().finish(key, activityId), REACTION_HALO_DURATION_MS);
      }
    };
    const observer = new IntersectionObserver(([entry]) => {
      visible = entry.isIntersecting && entry.intersectionRatio >= 0.5;
      update();
    }, { threshold: 0.5 });
    observer.observe(element);
    window.addEventListener("focus", update);
    window.addEventListener("blur", update);
    document.addEventListener("visibilitychange", update);
    return () => {
      observer.disconnect();
      if (timer) clearTimeout(timer);
      window.removeEventListener("focus", update);
      window.removeEventListener("blur", update);
      document.removeEventListener("visibilitychange", update);
    };
  }, [activityId, key]);

  return { ref, highlighted: Boolean(activityId) };
}
