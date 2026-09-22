interface PositionedMention {
  displayName: string;
  start: number;
  length: number;
}

export const resolveMentionPositions = <T extends PositionedMention>(text: string, mentions: T[]): T[] => {
  let searchFrom = 0;
  return [...mentions]
    .sort((left, right) => left.start - right.start)
    .flatMap((mention) => {
      const token = `@${mention.displayName}`;
      const start = text.indexOf(token, searchFrom);
      if (start < 0) return [];
      searchFrom = start + token.length;
      return [{ ...mention, start, length: token.length }];
    });
};
