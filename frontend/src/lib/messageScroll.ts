export function openingMessageScrollTop(scrollHeight: number, viewportHeight: number, unreadTop?: number): number {
  const bottom = Math.max(0, scrollHeight - viewportHeight);
  return unreadTop === undefined ? bottom : Math.max(0, Math.min(unreadTop, bottom));
}

export function isAtMessageListBottom(scrollHeight: number, scrollTop: number, viewportHeight: number): boolean {
  return scrollHeight - scrollTop - viewportHeight <= 2;
}
