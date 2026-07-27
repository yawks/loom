import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * Converts a Wails Time type to a JavaScript Date object.
 * The Time type from Wails is actually a string in TypeScript.
 */
export function timeToDate(time: any): Date {
  if (!time) {
    return new Date();
  }
  if (time instanceof Date) {
    return time;
  }
  if (typeof time === 'string') {
    return new Date(time);
  }
  // Fallback: convert to string first (handles Time type from Wails)
  return new Date(String(time));
}

/**
 * Transforms provider URL format <URL|text> to Markdown format [text](URL)
 * Also handles simple <URL> format (without pipe) -> [URL](URL)
 */
export function transformUrls(text: string): string {
  if (!text) return text;

  // Pattern to match <URL|text> or <URL>
  // This regex matches:
  // - <https://example.com|Link Text> -> [Link Text](https://example.com)
  // - <https://example.com> -> [https://example.com](https://example.com)
  return text.replace(/<([^|>]+)(?:\|([^>]+))?>/g, (_match, url, text) => {
    // If text is provided, use it; otherwise use the URL as text
    const linkText = text || url;
    return `[${linkText}](${url})`;
  });
}

/**
 * Escapes dashes at the start of lines to prevent them from being interpreted as Markdown lists
 */
export function escapeLeadingDashes(text: string): string {
  if (!text) return text;
  return text.replace(/^-/gm, String.raw`\-`);
}

/**
 * Fixes malformed code blocks in markdown text.
 * Ensures ``` are always alone on their line for proper markdown parsing.
 */
export function fixCodeBlocks(text: string): string {
  if (!text) return text;

  let fixed = text;

  // Languages that highlight.js recognises as valid fence identifiers.
  const KNOWN_LANGS = new Set([
    'bash', 'c', 'clojure', 'cpp', 'cs', 'css', 'dart', 'dockerfile',
    'elixir', 'erlang', 'go', 'graphql', 'groovy', 'haskell', 'html',
    'java', 'javascript', 'js', 'json', 'jsx', 'julia', 'kotlin', 'kt',
    'lua', 'makefile', 'markdown', 'matlab', 'md', 'nginx', 'objc',
    'objectivec', 'perl', 'php', 'plain', 'py', 'python', 'r', 'rb',
    'ruby', 'rust', 'sass', 'scala', 'scss', 'sh', 'shell', 'sql',
    'svelte', 'swift', 'text', 'ts', 'tsx', 'typescript', 'vue',
    'xml', 'yaml', 'yml', 'zsh',
  ]);

  // Step 1: ``` followed immediately by a non-word char → insert newline.
  fixed = fixed.replace(/```([^\w\s][^\r\n]*)/g, (_match, rest) => '```\n' + rest);

  // Step 2: Ensure closing ``` are alone on their line.
  fixed = fixed.replace(/([^\n])```(\s|$)/g, '$1\n```$2');

  // Step 3A: ```LANG immediately followed by punctuation (e.g. ```js// comment).
  // [^\w\s] excludes word chars so (\w+) captures the full word without backtracking.
  fixed = fixed.replace(/```(\w+)([^\w\s])/g, (_match, lang, nextChar) => {
    return '```' + lang + '\n' + nextChar;
  });

  // Step 3B: ``` or ```WORD followed by spaces then code on the same line.
  // If WORD is not a known language (e.g. "function", "var"), treat it as code content.
  fixed = fixed.replace(/```(\w*)[ \t]+(\S)/g, (_match, lang, nextChar) => {
    if (lang && !KNOWN_LANGS.has(lang.toLowerCase())) {
      return '```\n' + lang + ' ' + nextChar;
    }
    return '```' + lang + '\n' + nextChar;
  });

  return fixed;
}

const URL_RE = /https?:\/\/[^\s<>"'\])​]+/;

/** Returns the first http/https URL found in text, or null. */
export function extractFirstUrl(text: string): string | null {
  const m = URL_RE.exec(text);
  return m ? m[0].replace(/[.,;!?]+$/, "") : null;
}
