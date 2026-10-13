export function openingMessageScrollTop(scrollHeight: number, viewportHeight: number, unreadTop?: number): number {
  const bottom = Math.max(0, scrollHeight - viewportHeight);
  return unreadTop === undefined ? bottom : Math.max(0, Math.min(unreadTop, bottom));
}
