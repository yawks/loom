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
 * Transforms Slack URL format <URL|text> to Markdown format [text](URL)
 * Also handles simple <URL> format (without pipe) -> [URL](URL)
 */
export function transformSlackUrls(text: string): string {
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
  
  // Replace dashes at the start of lines with escaped dashes
  // This prevents Markdown from interpreting them as list items
  return text.replace(/^-/gm, '\\-');
}

/**
 * Fixes malformed code blocks in markdown text.
 * Ensures ``` are always alone on their line for proper markdown parsing.
 */
export function fixCodeBlocks(text: string): string {
  if (!text) return text;
  
  let fixed = text;
  
  // Step 1: Fix opening ``` followed by invalid characters (like ```/** or ```*)
  // Replace ```X where X is not a valid language identifier with just ```
  // Valid language identifiers are alphanumeric (javascript, python, etc.)
  // Invalid ones start with special chars like /, *, etc.
  fixed = fixed.replace(/```([^\w\s\r\n][^\r\n]*)/g, (_match, invalidPart) => {
    // Return ``` alone on a line, then the invalid part becomes part of the code
    return '```\n' + invalidPart;
  });
  
  // Step 2: Ensure closing ``` are alone on their line
  // If there's text before the closing ```, add a newline
  fixed = fixed.replace(/([^\n])```(\s|$)/g, '$1\n```$2');
  
  // Step 3: Ensure opening ``` are alone on their line  
  // If there's text after the language identifier (or after ``` if no language), ensure newline
  // Match ``` followed by optional word (language) and then non-whitespace on the same line
  fixed = fixed.replace(/```(\w+)?([^\s\r\n])/g, (_match, lang, nextChar) => {
    const language = lang || '';
    return '```' + language + '\n' + nextChar;
  });
  
  return fixed;
}
