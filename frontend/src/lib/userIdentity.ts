/** Returns a stable comparison key for IDs emitted in different provider forms. */
export function canonicalUserId(userId?: string): string {
  return (userId ?? "")
    .trim()
    .toLocaleLowerCase("en-US");
}

export function sameUserId(left?: string, right?: string): boolean {
  const leftKey = canonicalUserId(left);
  return leftKey.length > 0 && leftKey === canonicalUserId(right);
}
