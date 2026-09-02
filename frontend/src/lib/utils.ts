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

  // Some rich-text converters escape Markdown emphasis markers as literal text. Only
  // repair complete pairs so isolated escaped asterisks keep their meaning.
  const repairedEmphasis = text.replace(
    /\\\*\\\*([^\n]*?\S)[ \t]*\\\*\\\*/g,
    (_match, content) => `**${content}**`,
  );

  // Rich-text converters can serialize a pasted link whose label already
  // contains Markdown, followed by a second escaped copy. Collapse that shape
  // before remark sees it. Comparing normalized URLs keeps this deliberately
  // narrow and avoids rewriting ordinary nested brackets.
  const duplicatedRichTextLink = /\[\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)\]\\\(\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)\)/g;
  const normalizeComparedUrl = (value: string) => value
    .replace(/\\([_&])/g, "$1")
    .replace(/&amp;/gi, "&");
  const deduplicatedText = repairedEmphasis.replace(
    duplicatedRichTextLink,
    (match, firstLabel, firstUrl, secondLabel, secondUrl) => {
      const urls = [firstLabel, firstUrl, secondLabel, secondUrl].map(normalizeComparedUrl);
      if (!urls.every((url) => url === urls[0])) return match;
      return `[${secondLabel}](${secondUrl})`;
    },
  );

  // Repair messages that were previously transformed twice, resulting in
  // `[label]([URL](URL))`. This runs at display time, so it also fixes
  // already-cached history.
  const repairedText = deduplicatedText.replace(
    /\[([^\]\n]+)\]\(\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)\)/g,
    (_match, label, _nestedLabel, url) => `[${label}](${url})`,
  );

  // Pattern to match <URL|text> or <URL>
  // This regex matches:
  // - <https://example.com|Link Text> -> [Link Text](https://example.com)
  // - <https://example.com> -> [https://example.com](https://example.com)
  return repairedText.replace(/<([^|>]+)(?:\|([^>]+))?>/g, (match, url, linkText, offset, source) => {
    // Markdown permits a link destination to be enclosed in angle brackets:
    // `[label](<https://example.com/...>)`. It is already valid Markdown, so
    // do not apply the provider-format conversion a second time.
    if (source.slice(0, offset).endsWith("](")) {
      return match;
    }

    // If text is provided, use it; otherwise use the URL as text
    const displayText = linkText || url;
    return `[${displayText}](${url})`;
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

/**
 * Adds a language to unlabeled Markdown fences when the syntax provides a
 * strong signal. highlight.js otherwise commonly mistakes short Java/C-like
 * snippets for SQL. Ambiguous blocks remain unlabeled and use its normal
 * automatic detection.
 */
export function annotateUnlabeledCodeFences(text: string): string {
  if (!text.includes("```")) return text;

  return text.replace(/(^|\n)```[ \t]*\n([\s\S]*?)\n```(?=\n|$)/g, (match, prefix, code) => {
    const language = inferCodeLanguage(code);
    return language ? `${prefix}\`\`\`${language}\n${code}\n\`\`\`` : match;
  });
}

function inferCodeLanguage(code: string): string | null {
  const signals: Array<[string, RegExp[]]> = [
    ["json", [/^\s*[\[{]/, /"[^"\n]+"\s*:/, /\b(?:true|false|null)\b/]],
    ["java", [
      /\b(?:public|private|protected|class|interface|extends|implements|throws|package|import)\b/,
      /\b(?:byte|short|int|long|float|double|boolean|char|String)\s+(?:\[\]\s*)?[A-Za-z_$][\w$]*/,
      /\b[A-Z][\w$]*(?:<[^;\n]+>)?\[\]\s+[A-Za-z_$][\w$]*/,
    ]],
    ["typescript", [/\b(?:interface|type|enum|implements|readonly|keyof)\b/, /:\s*(?:string|number|boolean|unknown|never|void)\b/]],
    ["javascript", [
      /\bfunction\s+[A-Za-z_$][\w$]*\s*\(/,
      /\b(?:const|let|var)\s+[A-Za-z_$][\w$]*\s*(?:=|;)/,
      /\b(?:undefined|typeof|async|await)\b/,
      /=>/,
    ]],
    ["python", [/^\s*(?:def|class|from|import)\s+/m, /:\s*(?:#.*)?$/m, /\b(?:None|True|False|elif)\b/]],
    ["go", [/^\s*(?:package|func|type)\s+/m, /:=/, /\b(?:chan|defer|goroutine)\b/]],
    ["rust", [/\b(?:fn|let mut|impl|trait|pub struct|match)\b/, /&mut\b/, /::/]],
    ["sql", [/\bSELECT\b[\s\S]+\bFROM\b/i, /\b(?:INSERT INTO|UPDATE|DELETE FROM|CREATE TABLE|ALTER TABLE)\b/i]],
    ["bash", [/^\s*#!.*\b(?:ba|z)?sh\b/m, /^\s*(?:export|source|sudo)\s+/m, /\$\{[^}]+\}/]],
  ];

  let best: { language: string; score: number } | null = null;
  for (const [language, patterns] of signals) {
    const score = patterns.reduce((total, pattern) => total + (pattern.test(code) ? 1 : 0), 0);
    if (score >= 2 && (!best || score > best.score)) best = { language, score };
  }
  return best?.language ?? null;
}

const URL_RE = /https?:\/\/[^\s<>"'\])​]+/;

/** Returns the first http/https URL found in text, or null. */
export function extractFirstUrl(text: string): string | null {
  const m = URL_RE.exec(text);
  return m ? m[0].replace(/[.,;!?]+$/, "") : null;
}
