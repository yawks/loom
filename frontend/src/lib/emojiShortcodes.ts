// A colon immediately before a URL must not turn its scheme into an emoji
// (for example "voir :https://example.com"). Return a fresh global expression
// so independent render passes do not share lastIndex state.
export function emojiShortcodePattern(): RegExp {
  return /(?<![a-zA-Z0-9]):([a-zA-Z0-9_+-]+):(?![a-zA-Z0-9]|\/\/)/g;
}
