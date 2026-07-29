import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import React, { type ReactElement, useMemo, memo } from "react";
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

interface SlackInlineQuote {
  sender: string;
  quotedText: string;
  body: string;
}

// Slack serializes Loom's quoted replies as a Markdown block quote. Parse it
// before generic Markdown rendering so replies remain readable even when the
// cached message has lost its quotedMessageId/quotedBody metadata.
function parseSlackInlineQuote(text: string): SlackInlineQuote | null {
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

function buildComponents(isFromMe: boolean, preview: boolean, isInline: boolean): Components {
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
              const jid = mentionURL.searchParams.get("jid");
              if (!jid) return;

              const { metaContacts, setSelectedContact } = useAppStore.getState();
              const contact = metaContacts.find((candidate) =>
                candidate.linkedAccounts.some(
                  (account) =>
                    account.protocol === "whatsapp" &&
                    (account.userId === jid || account.conversationId === jid)
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
    p: ({ ...props }) => (isInline ? <span {...props} /> : <p className="m-0" {...props} />),
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
}: MessageTextProps) {
  useRenderCount("MessageText", { textLength: text?.length, preview });
  const slackInlineQuote = useMemo(() => parseSlackInlineQuote(text), [text]);

  const parsedContent = useMemo(() => {
    if (!text) return null;

    // Preprocessing
    let processedText = transformUrls(htmlFragmentToText(text));
    // Last-resort compatibility for cached Slack replies that reach this
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

    // Splitting around custom emoji shortcodes breaks Markdown delimiters when
    // an emoji appears between **...** or *...*. In formatted messages,
    // preserve the complete Markdown document; rendering the emphasis takes
    // precedence over replacing colon shortcodes.
    if (/(\*\*|__|~~|`|\[[^\]]+\]\()/.test(textWithoutSkinTones)) {
      return textWithoutSkinTones;
    }

    // Skip emoji parsing inside code blocks
    const hasCodeBlocks = /```[\s\S]*?```/g.test(textWithoutSkinTones);
    if (hasCodeBlocks) {
      return textWithoutSkinTones;
    }

    // Markdown constructs such as Slack's quoted replies span multiple lines.
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
    () => buildComponents(isFromMe, preview, false),
    [isFromMe, preview]
  );
  const inlineComponents = useMemo(
    () => buildComponents(isFromMe, preview, true),
    [isFromMe, preview]
  );

  const remarkPlugins = useMemo(
    () => (preview ? [remarkGfm] : [remarkGfm, remarkBreaks]),
    [preview]
  );

  if (!parsedContent) return null;

  const renderMarkdown = (content: string, isInline = false) => (
    <ReactMarkdown
      remarkPlugins={remarkPlugins}
      rehypePlugins={[[rehypeHighlight, { detect: true }]]}
      components={isInline ? inlineComponents : blockComponents}
      urlTransform={(url) => (url.startsWith("loom://") ? url : defaultUrlTransform(url))}
    >
      {content}
    </ReactMarkdown>
  );

  if (slackInlineQuote) {
    return (
      <div className={cn(className, "max-w-full overflow-hidden space-y-1")}>
        <blockquote className="border-l-2 border-current/40 pl-3 text-sm opacity-90">
          <div className="font-semibold">{slackInlineQuote.sender}</div>
          {renderMarkdown(slackInlineQuote.quotedText)}
        </blockquote>
        {slackInlineQuote.body && renderMarkdown(slackInlineQuote.body)}
      </div>
    );
  }

  if (typeof parsedContent === "string") {
    return <div className={cn(className, "max-w-full overflow-hidden")}>{renderMarkdown(parsedContent)}</div>;
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
