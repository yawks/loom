import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import React, { type ReactElement, useMemo, memo } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import { Emoji } from "./Emoji";
import { CodeBlock } from "./CodeBlock";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import { transformUrls, escapeLeadingDashes, fixCodeBlocks } from "../lib/utils";
import { cleanEmoji } from "@/lib/userDisplayNames";
import { cn } from "@/lib/utils";
import { useRenderCount } from "@/hooks/useRenderCount";

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
            if (href) BrowserOpenURL(href);
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
    pre: ({ children }) => <CodeBlock isFromMe={isFromMe}>{children}</CodeBlock>,
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

  const parsedContent = useMemo(() => {
    if (!text) return null;

    // Preprocessing
    let processedText = transformUrls(text);
    processedText = fixCodeBlocks(processedText);

    if (preview) {
      processedText = processedText.replace(/\n+/g, " ");
    } else {
      processedText = escapeLeadingDashes(processedText);
    }

    const textWithoutSkinTones = cleanEmoji(processedText);

    // Skip emoji parsing inside code blocks
    const hasCodeBlocks = /```[\s\S]*?```/g.test(textWithoutSkinTones);
    if (hasCodeBlocks) {
      return textWithoutSkinTones;
    }

    const parts: (string | ReactElement)[] = [];
    let lastIndex = 0;

    const emojiPattern = /:([a-zA-Z0-9_+-]+):/g;
    let match: RegExpExecArray | null;
    while (true) {
      match = emojiPattern.exec(textWithoutSkinTones);
      if (match === null) break;

      const matchIndex = match.index;
      const matchText = match[0];

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
    >
      {content}
    </ReactMarkdown>
  );

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
