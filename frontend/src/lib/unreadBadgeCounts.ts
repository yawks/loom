export interface UnreadBadgeCounts {
  tracked: number;
  untracked: number;
  total: number;
}

export const emptyUnreadBadgeCounts = (): UnreadBadgeCounts => ({
  tracked: 0,
  untracked: 0,
  total: 0,
});

export const countUnreadMessages = (
  conversationState: Record<string, boolean> | undefined
): number =>
  conversationState
    ? Object.entries(conversationState).filter(
        ([key, isRead]) => !key.startsWith("_") && !isRead
      ).length
    : 0;

export const addUnreadCount = (
  counts: UnreadBadgeCounts,
  unread: number,
  isTracked: boolean
): void => {
  if (unread <= 0) return;
  counts.total += unread;
  if (isTracked) {
    counts.tracked += unread;
  } else {
    counts.untracked += unread;
  }
};

export const formatUnreadCount = (count: number): string =>
  count > 99 ? "99+" : count.toString();
