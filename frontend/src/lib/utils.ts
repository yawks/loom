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
