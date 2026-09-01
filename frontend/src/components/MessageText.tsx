import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import React, { type CSSProperties, type ReactElement, type ReactNode, useMemo, memo } from "react";
import ReactMarkdown, { defaultUrlTransform, type Components } from "react-markdown";
import { Emoji } from "./Emoji";
import { CodeBlock } from "./CodeBlock";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import { transformUrls, fixCodeBlocks } from "../lib/utils";
import { cleanEmoji } from "@/lib/userDisplayNames";
import { cn } from "@/lib/utils";
import { useRenderCount } from "@/hooks/useRenderCount";
import { useAppStore } from "@/lib/store";
import { htmlFragmentToText } from "@/lib/messageUtils";
import type { PluggableList } from "unified";

interface SerializedInlineQuote {
  sender: string;
  quotedText: string;
  body: string;
}

const SAFE_RICH_COLOR = /^(?:#[0-9a-f]{3,8}|(?:rgb|rgba|hsl|hsla)\([0-9.,% ]+\)|[a-z]+)$/i;

interface HastNode {
  type: string;
  value?: string;
  tagName?: string;
  properties?: Record<string, unknown>;
  children?: HastNode[];
}

function rehypeSearchHighlight(query: string) {
  const escapedQuery = query.trim().replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const matcher = escapedQuery ? new RegExp(`(${escapedQuery})`, "giu") : null;

  return (tree: HastNode) => {
    if (!matcher) return;
    const visit = (node: HastNode) => {
      if (!node.children || node.tagName === "mark") return;
      node.children = node.children.flatMap((child) => {
        if (child.type !== "text" || !child.value) {
          visit(child);
          return [child];
        }
        const parts = child.value.split(matcher);
        if (parts.length === 1) return [child];
        return parts.filter(Boolean).map((part, index) =>
          index % 2 === 1
            ? {
                type: "element",
                tagName: "mark",
                properties: {
                  className: ["rounded-sm", "bg-yellow-300", "px-0.5", "text-inherit", "dark:bg-yellow-500/50"],
                },
                children: [{ type: "text", value: part }],
              }
            : { type: "text", value: part }
        );
      });
    };
    visit(tree);
  };
}

function richTextStyle(element: Element): CSSProperties {
  const style: CSSProperties = {};
  const color = element.getAttribute("color")?.trim();
  const background = element.getAttribute("background")?.trim();
  if (color && SAFE_RICH_COLOR.test(color)) style.color = color;
  if (background && SAFE_RICH_COLOR.test(background)) style.backgroundColor = background;

  const size = element.getAttribute("size")?.trim().match(/^([0-9]+(?:\.[0-9]+)?)(px|pt|em|rem|%)$/i);
  if (size) {
    const value = Number(size[1]);
    const unit = size[2].toLowerCase();
    const limits: Record<string, [number, number]> = {
      px: [8, 48], pt: [6, 36], em: [0.5, 3], rem: [0.5, 3], "%": [50, 300],
    };
    const [minimum, maximum] = limits[unit];
    style.fontSize = `${Math.min(maximum, Math.max(minimum, value))}${unit}`;
  }
  if (element.getAttribute("underline") === "true") style.textDecorationLine = "underline";
  return style;
}

// Legacy Loom rows may contain a quoted reply serialized as a Markdown block.
// Parse the format before generic rendering when canonical quote fields were lost.
function parseSerializedInlineQuote(text: string): SerializedInlineQuote | null {
  const lines = text
    .replace(/&gt;|&#(?:0*62);/gi, ">")
    .replace(/\r\n/g, "\n")
    .replace(/^[\s\u200B]+/, "")
    .split("\n");
  const header = lines[0]?.match(/^>\s*\*([^*]+)\*\s*$/);
  if (!header) return null;

  const quotedLines: string[] = [];
  let index = 1;
  while (index < lines.length && /^>\s?/.test(lines[index])) {
    quotedLines.push(lines[index].replace(/^>\s?/, ""));
    index += 1;
  }
  if (quotedLines.length === 0) return null;

  return {
    sender: header[1].trim(),
    quotedText: quotedLines.join("\n"),
    body: lines.slice(index).join("\n").trimStart(),
  };
}

function buildComponents(isFromMe: boolean, preview: boolean, isInline: boolean, providerInstanceId?: string, emojiSize = 16): Components {
  return {
    a: ({ href, children, ...props }) => {
      if (preview) return <span {...props}>{children}</span>;
      return (
        <a
          {...props}
          href={href}
          onClick={(e) => {
            e.preventDefault();
            if (!href) return;

            if (href.startsWith("loom://conversation")) {
              const mentionURL = new URL(href);
              // "jid" is kept as a compatibility fallback for messages already
              // stored before internal conversation links became provider-neutral.
              const accountId =
                mentionURL.searchParams.get("accountId") ??
                mentionURL.searchParams.get("jid");
              const instanceId = mentionURL.searchParams.get("instanceId");
              if (!accountId) return;

              const { metaContacts, setSelectedContact } = useAppStore.getState();
              const contact = metaContacts.find((candidate) =>
                candidate.linkedAccounts.some(
                  (account) =>
                    (!instanceId || account.providerInstanceId === instanceId) &&
                    (account.userId === accountId || account.conversationId === accountId)
                )
              );
              if (contact) setSelectedContact(contact);
              return;
            }

            BrowserOpenURL(href);
          }}
          className={cn(
            "cursor-pointer hover:underline",
            "text-blue-600 dark:text-blue-400 hover:text-blue-700 dark:hover:text-blue-300"
          )}
        >
          {children}
        </a>
      );
    },
    strong: ({ ...props }) => <strong className="font-bold" {...props} />,
    em: ({ ...props }) => <em className="italic" {...props} />,
    pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
    code: ({ className, children, ...props }) => {
      const isBlock = className?.includes("hljs");
      if (isBlock) return <code className={className} {...props}>{children}</code>;
      return (
        <code
          className={cn("px-1 py-0.5 rounded text-sm font-mono", isFromMe ? "bg-white/20" : "bg-muted")}
          {...props}
        >
          {children}
        </code>
      );
    },
    img: ({ src, alt, ...props }) => {
      if (src?.startsWith("loom-emoji://")) {
        const emojiName = decodeURIComponent(src.slice("loom-emoji://".length));
        return (
          <Emoji
            emoji={`:${emojiName}:`}
            providerInstanceId={providerInstanceId}
            size={emojiSize}
            className="inline align-baseline mx-0.5"
          />
        );
      }
      return <img src={src} alt={alt ?? ""} {...props} />;
    },
    p: ({ className, ...props }) => (
      isInline
        ? <span className={className} {...props} />
        : <p className={cn("m-0", !preview && "[&+p]:mt-[1.5em]", className)} {...props} />
    ),
    br: ({ ...props }) => (preview ? <span> </span> : <br {...props} />),
    div: ({ ...props }) => (isInline ? <span {...props} /> : <div {...props} />),
    blockquote: ({ ...props }) => (
      <blockquote className="my-1 border-l-2 border-current/40 pl-3 italic opacity-90" {...props} />
    ),
    ul: ({ ...props }) => <ul className="list-disc pl-5 my-1 space-y-0.5" {...props} />,
    ol: ({ ...props }) => <ol className="list-decimal pl-5 my-1 space-y-0.5" {...props} />,
    li: ({ ...props }) => <li className="leading-snug" {...props} />,
    table: ({ ...props }) => (
      <div className="my-2 max-w-full overflow-x-auto">
        <table className="border-collapse text-sm" {...props} />
      </div>
    ),
    thead: ({ ...props }) => <thead className="bg-black/5 dark:bg-white/10" {...props} />,
    th: ({ ...props }) => <th className="border border-current/20 px-2 py-1 text-left font-semibold" {...props} />,
    td: ({ ...props }) => <td className="border border-current/20 px-2 py-1 align-top" {...props} />,
  };
}

interface MessageTextProps {
  text: string; // Message text that may contain emojis (e.g., ":calendar:")
  providerInstanceId?: string; // Provider instance ID
  className?: string;
  emojiSize?: number; // Size for emojis in pixels (default: 16)
  preview?: boolean; // If true, render as preview (no blue links, single line)
  isFromMe?: boolean; // If true, message is from current user
  highlightQuery?: string; // Literal text to emphasize in search previews
}

/**
 * Generic component to parse and display message text with emojis and Markdown.
 * URLs are clickable and open in the browser.
 */
export const MessageText = memo(function MessageText({
  text,
  providerInstanceId,
  className = "",
  emojiSize = 16,
  preview = false,
  isFromMe = false,
  highlightQuery = "",
}: MessageTextProps) {
  useRenderCount("MessageText", { textLength: text?.length, preview });
  const serializedInlineQuote = useMemo(() => parseSerializedInlineQuote(text), [text]);

  const parsedContent = useMemo(() => {
    if (!text) return null;

    // Preprocessing
    // Preserve Loom's explicit underline extension while stripping provider
    // HTML. Standard Markdown intentionally has no underline syntax.
    const richTags: string[] = [];
    const richProtected = text.replace(/<\/?loom-style\b[^>]*>/gi, (tag) => {
      const index = richTags.push(tag) - 1;
      return `LOOM_RICH_TAG_${index}_`;
    });
    const underlineProtected = richProtected
      .replace(/<u>/gi, "LOOM_UNDERLINE_OPEN")
      .replace(/<\/u>/gi, "LOOM_UNDERLINE_CLOSE");
    let processedText = transformUrls(htmlFragmentToText(underlineProtected))
      .replaceAll("LOOM_UNDERLINE_OPEN", "<u>")
      .replaceAll("LOOM_UNDERLINE_CLOSE", "</u>")
      .replace(/LOOM_RICH_TAG_(\d+)_/g, (_, index) => richTags[Number(index)] ?? "");
    // Last-resort compatibility for cached serialized replies that reach this
    // component without reply metadata. Do this before emoji splitting and
    // Markdown parsing so neither stage can expose the protocol's `>` syntax.
    if (/^\s*>\s*\*[^*\n]+\*\s*$/m.test(processedText)) {
      processedText = processedText.replace(/^\s*>\s?/gm, "");
    }
    processedText = fixCodeBlocks(processedText);

    if (preview) {
      processedText = processedText.replace(/\n+/g, " ");
    }

    const textWithoutSkinTones = cleanEmoji(processedText);

    // Keep formatted Markdown as one document, but encode emoji shortcodes as
    // synthetic inline images. The custom `img` renderer above turns them into
    // Emoji components without breaking emphasis or links.
    if (/(\*\*|__|~~|`|\[[^\]]+\]\()/.test(textWithoutSkinTones)) {
      return textWithoutSkinTones.replace(
        /:([a-zA-Z0-9_+-]+):/g,
        (match, name, offset, source) => {
          const before = source.slice(0, offset);
          const currentLine = before.slice(before.lastIndexOf("\n") + 1);
          if (/https?:\/\/\S*$/.test(currentLine)) return match;
          return `![${match}](loom-emoji://${encodeURIComponent(name)})`;
        },
      );
    }

    // Skip emoji parsing inside code blocks
    const hasCodeBlocks = /```[\s\S]*?```/g.test(textWithoutSkinTones);
    if (hasCodeBlocks) {
      return textWithoutSkinTones;
    }

    // Markdown constructs such as serialized quoted replies span multiple lines.
    // Passing each line independently makes react-markdown treat `>` as plain
    // text, so keep the complete document intact when no emoji replacement is
    // needed.
    const emojiPattern = /:([a-zA-Z0-9_+-]+):/g;
    // SharePoint (among others) uses URL paths such as `/:p:/`. Do not turn
    // those path segments into custom emojis before Markdown sees the URL.
    const urlRanges = Array.from(textWithoutSkinTones.matchAll(/https?:\/\/[^\s<>"']+/g))
      .map((match) => ({ start: match.index ?? 0, end: (match.index ?? 0) + match[0].length }));
    const isInsideUrl = (index: number) =>
      urlRanges.some(({ start, end }) => index >= start && index < end);
    const hasEmojiOutsideUrl = Array.from(textWithoutSkinTones.matchAll(emojiPattern))
      .some((match) => !isInsideUrl(match.index ?? 0));

    if (!hasEmojiOutsideUrl) {
      return textWithoutSkinTones;
    }
    emojiPattern.lastIndex = 0;

    const parts: (string | ReactElement)[] = [];
    let lastIndex = 0;

    let match: RegExpExecArray | null;
    while (true) {
      match = emojiPattern.exec(textWithoutSkinTones);
      if (match === null) break;

      const matchIndex = match.index;
      const matchText = match[0];

      if (isInsideUrl(matchIndex)) {
        continue;
      }

      if (matchIndex > lastIndex) {
        const textBefore = textWithoutSkinTones.substring(lastIndex, matchIndex);
        const lines = textBefore.split("\n");
        lines.forEach((line: string, lineIdx: number) => {
          if (lineIdx > 0) parts.push(<br key={`br-${matchIndex}-${lineIdx}`} />);
          if (line) parts.push(line);
        });
      }

      const fullEmojiName = matchText;
      parts.push(
        <Emoji
          key={`emoji-${matchIndex}`}
          emoji={fullEmojiName}
          providerInstanceId={providerInstanceId}
          size={emojiSize}
          className="inline align-baseline mx-0.5"
        />
      );

      lastIndex = matchIndex + matchText.length;
    }

    if (lastIndex < textWithoutSkinTones.length) {
      const remainingText = textWithoutSkinTones.substring(lastIndex);
      const lines = remainingText.split("\n");
      lines.forEach((line: string, lineIdx: number) => {
        if (lineIdx > 0) parts.push(<br key={`br-end-${lineIdx}`} />);
        if (line) parts.push(line);
      });
    }

    return parts.length === 0 ? textWithoutSkinTones : parts;
  }, [text, providerInstanceId, emojiSize, preview]);

  const blockComponents = useMemo(
    () => buildComponents(isFromMe, preview, false, providerInstanceId, emojiSize),
    [isFromMe, preview, providerInstanceId, emojiSize]
  );
  const inlineComponents = useMemo(
    () => buildComponents(isFromMe, preview, true, providerInstanceId, emojiSize),
    [isFromMe, preview, providerInstanceId, emojiSize]
  );

  const remarkPlugins = useMemo(
    () => (preview ? [remarkGfm] : [remarkGfm, remarkBreaks]),
    [preview]
  );
  const rehypePlugins = useMemo<PluggableList>(
    () => [
      [rehypeHighlight, { detect: true }],
      [rehypeSearchHighlight, highlightQuery],
    ],
    [highlightQuery]
  );

  if (!parsedContent) return null;

  const renderMarkdownBase = (content: string, isInline = false) => (
    <ReactMarkdown
      remarkPlugins={remarkPlugins}
      rehypePlugins={rehypePlugins}
      components={isInline ? inlineComponents : blockComponents}
      urlTransform={(url) => (url.startsWith("loom://") || url.startsWith("loom-emoji://") ? url : defaultUrlTransform(url))}
    >
      {content}
    </ReactMarkdown>
  );

  const renderMarkdown = (content: string, isInline = false) => {
    if (!/<(?:u|loom-style)\b/i.test(content)) return renderMarkdownBase(content, isInline);


    // Parsing each rich-text element separately detaches a Markdown list marker
    // from the colored text that follows it (`- <loom-style>text</loom-style>`).
    // Keep rich-text lists as one React list and only parse the contents of each
    // item independently, so color remains inline with its bullet.
    const nonEmptyLines = content.split("\n").filter((line) => line.trim() !== "");
    const unorderedItems = nonEmptyLines.map((line) => line.match(/^\s*[-+]\s+(.+)$/));
    if (unorderedItems.length > 0 && unorderedItems.every(Boolean)) {
      return (
        <ul className="list-disc pl-5 my-1 space-y-0.5">
          {unorderedItems.map((match, index) => (
            <li className="leading-snug" key={index}>{renderMarkdown(match?.[1] ?? "", true)}</li>
          ))}
        </ul>
      );
    }
    const orderedItems = nonEmptyLines.map((line) => line.match(/^\s*\d+[.)]\s+(.+)$/));
    if (orderedItems.length > 0 && orderedItems.every(Boolean)) {
      return (
        <ol className="list-decimal pl-5 my-1 space-y-0.5">
          {orderedItems.map((match, index) => (
            <li className="leading-snug" key={index}>{renderMarkdown(match?.[1] ?? "", true)}</li>
          ))}
        </ol>
      );
    }

    const documentNode = new DOMParser().parseFromString(content, "text/html");
    const renderNodes = (nodes: NodeListOf<ChildNode> | ChildNode[], inline: boolean): ReactNode[] =>
      Array.from(nodes).map((node, index) => {
        if (node.nodeType === Node.TEXT_NODE) {
          const value = node.textContent ?? "";
          // Plain fragments already contain the exact spacing supplied by the
          // canonical message. Sending each one through react-markdown creates
          // a paragraph AST and can add separator whitespace around adjacent
          // inline rich-text elements.
          const needsMarkdown = /[\n*_~`<>]|https?:\/\/|loom-emoji:\/\//i.test(value) ||
            value.includes("[") || value.includes("]");
          if (!needsMarkdown) {
            return <React.Fragment key={index}>{value}</React.Fragment>;
          }
          return <React.Fragment key={index}>{renderMarkdownBase(value, inline)}</React.Fragment>;
        }
        if (node.nodeType !== Node.ELEMENT_NODE) return null;
        const element = node as Element;
        const children = renderNodes(element.childNodes, true);
        if (element.tagName.toLowerCase() === "u") return <u key={index}>{children}</u>;
        if (element.tagName.toLowerCase() === "loom-style") {
          return <span key={index} style={richTextStyle(element)}>{children}</span>;
        }
        return <React.Fragment key={index}>{children}</React.Fragment>;
      });
    // The DOM nodes here are fragments of one Markdown document, not separate
    // blocks. Rendering root text nodes in block mode makes react-markdown wrap
    // the text on either side of an inline rich tag in separate <p> elements
    // (for example `before <u>under</u> after`). Keep every fragment inline;
    // explicit Markdown line breaks are still handled by remark-breaks.
    return <>{renderNodes(documentNode.body.childNodes, true)}</>;
  };

  if (serializedInlineQuote) {
    return (
      <div className={cn(className, "max-w-full overflow-hidden space-y-1")}>
        <blockquote className="border-l-2 border-current/40 pl-3 text-sm opacity-90">
          <div className="font-semibold">{serializedInlineQuote.sender}</div>
          {renderMarkdown(serializedInlineQuote.quotedText)}
        </blockquote>
        {serializedInlineQuote.body && renderMarkdown(serializedInlineQuote.body)}
      </div>
    );
  }

  if (typeof parsedContent === "string") {
    return (
      <div className={cn(className, "max-w-full overflow-hidden")}>
        {renderMarkdown(parsedContent, preview)}
      </div>
    );
  }

  return (
    <div className={cn(className, "max-w-full overflow-hidden")}>
      {parsedContent.map((part, index) => (
        <React.Fragment key={index}>
          {typeof part === "string" ? renderMarkdown(part, true) : part}
        </React.Fragment>
      ))}
    </div>
  );
});
