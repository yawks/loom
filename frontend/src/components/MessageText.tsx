import { cn, escapeLeadingDashes, fixCodeBlocks, transformSlackUrls } from "../lib/utils";

import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { CodeBlock } from "./CodeBlock";
import ReactMarkdown from "react-markdown";
import { SlackMessageText } from "./SlackMessageText";
import { memo } from "react";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";
import { useRenderCount } from "@/hooks/useRenderCount";

interface MessageTextProps {
  text: string;
  providerInstanceId?: string;
  className?: string;
  emojiSize?: number;
  isSlack?: boolean; // If true, use Slack emoji parsing
  preview?: boolean; // If true, render as preview (no blue links, single line)
  isFromMe?: boolean; // If true, message is from current user (for link color contrast)
}

/**
 * Component to render message text with Markdown support.
 * For Slack messages, it also handles emoji parsing.
 * URLs are clickable and open in the browser.
 */
export const MessageText = memo(function MessageText({
  text,
  providerInstanceId,
  className = "",
  emojiSize = 16,
  isSlack = false,
  preview = false,
  isFromMe = false,
}: MessageTextProps) {
  // Debug: Track re-renders
  useRenderCount("MessageText", { 
    textLength: text?.length, 
    isSlack, 
    preview
  });
  if (!text || text.trim() === "") {
    return null;
  }

  // Transform Slack URLs to Markdown format
  let processedText = transformSlackUrls(text);
  
  // Fix malformed code blocks (e.g., ```/** -> ```javascript)
  processedText = fixCodeBlocks(processedText);

  // In preview mode, replace newlines with spaces to keep content on one line
  if (preview) {
    processedText = processedText.replace(/\n+/g, " ");
  } else {
    // Escape dashes at the start of lines to prevent them from being interpreted as Markdown lists
    processedText = escapeLeadingDashes(processedText);
  }

  // For Slack messages, use SlackMessageText which handles emojis
  // But we need to transform URLs first
  if (isSlack) {
    return (
      <SlackMessageText
        text={processedText}
        providerInstanceId={providerInstanceId}
        className={className}
        emojiSize={emojiSize}
        preview={preview}
        isFromMe={isFromMe}
      />
    );
  }

  // For non-Slack messages, render markdown directly
  return (
    <div className={cn(className, "max-w-full overflow-hidden")}>
      <ReactMarkdown
        remarkPlugins={preview ? [remarkGfm] : [remarkGfm, remarkBreaks]}
        components={{
          // Make links open in browser (or render as plain text in preview mode)
          a: ({ href, children, ...props }) => {
            if (preview) {
              // In preview mode, render links as plain text
              return <span {...props}>{children}</span>;
            }
            const handleClick = (e: React.MouseEvent<HTMLAnchorElement>) => {
              e.preventDefault();
              if (href) {
                BrowserOpenURL(href);
              }
            };
            return (
              <a
                {...props}
                href={href}
                onClick={handleClick}
                className={
                  isFromMe
                    ? "text-blue-100 hover:text-white hover:underline cursor-pointer"
                    : "text-blue-600 dark:text-blue-400 hover:text-blue-700 dark:hover:text-blue-300 hover:underline cursor-pointer"
                }
              >
                {children}
              </a>
            );
          },
          // Style for bold text
          strong: ({ ...props }) => (
            <strong className="font-bold" {...props} />
          ),
          // Style for italic text
          em: ({ ...props }) => (
            <em className="italic" {...props} />
          ),
          // Style for code with syntax highlighting
          code: ({ className, children, ...props }) => {
            const isInline = !className?.includes("language-");
            return (
              <CodeBlock
                className={className}
                inline={isInline}
                isFromMe={isFromMe}
                {...props}
              >
                {String(children).replace(/\n$/, "")}
              </CodeBlock>
            );
          },
          // Preserve line breaks (but not in preview mode)
          p: ({ ...props }) => (
            <p className="m-0" {...props} />
          ),
          br: ({ ...props }) => {
            if (preview) {
              // In preview mode, replace line breaks with spaces to keep content on one line
              return <span> </span>;
            }
            return <br {...props} />;
          },
        }}
      >
        {processedText}
      </ReactMarkdown>
    </div>
  );
});
