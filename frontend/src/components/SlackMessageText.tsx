import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import React, { type ReactElement, useMemo } from "react";
import ReactMarkdown from "react-markdown";
import { SlackEmoji } from "./SlackEmoji";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";
import { transformSlackUrls } from "../lib/utils";
import { cleanSlackEmoji } from "../lib/userDisplayNames";

interface SlackMessageTextProps {
  text: string; // Message text that may contain Slack emojis/avatars (e.g., ":calendar:", ":avatar_name:")
  providerInstanceId?: string; // Provider instance ID (e.g., "slack-1")
  className?: string;
  emojiSize?: number; // Size for emojis/avatars in pixels (default: 16)
  preview?: boolean; // If true, render as preview (no blue links, single line)
  isFromMe?: boolean; // If true, message is from current user (for link color contrast)
}

/**
 * Component to parse and display Slack message text with emojis, avatars, and Markdown.
 * Replaces patterns like :emoji_name: or :avatar_name: with SlackEmoji components.
 * Also renders Markdown formatting (bold, italic, links, etc.)
 */
export function SlackMessageText({
  text,
  providerInstanceId,
  className = "",
  emojiSize = 16,
  preview = false,
  isFromMe = false,
}: SlackMessageTextProps) {
  const parsedContent = useMemo(() => {
    if (!text) return null;

    // First, transform Slack URLs to Markdown format
    let processedText = transformSlackUrls(text);

    // In preview mode, replace newlines with spaces to keep content on one line
    if (preview) {
      processedText = processedText.replace(/\n+/g, " ");
    }

    // Remove skin-tone modifiers from the text (they should not be displayed)
    // Handles cases like ":+1::skin-tone-2:" -> ":+1:"
    const textWithoutSkinTones = cleanSlackEmoji(processedText);

    // Use the text without skin tones for processing
    const textWithPreservedNewlines = textWithoutSkinTones;

    const parts: (string | ReactElement)[] = [];
    let lastIndex = 0;

    // Find all emoji matches first to avoid regex state issues
    const matches: Array<{ index: number; match: RegExpExecArray }> = [];
    const emojiPattern = /:([a-zA-Z0-9_+-]+):/g;
    let tempMatch;
    while ((tempMatch = emojiPattern.exec(textWithPreservedNewlines)) !== null) {
      matches.push({ index: tempMatch.index, match: tempMatch });
    }

    // Process matches
    for (const { index, match: emojiMatch } of matches) {
      
      // Add text before the match (this includes any newlines)
      // Convert newlines to <br /> elements so they render correctly
      if (index > lastIndex) {
        const textBefore = textWithPreservedNewlines.substring(lastIndex, index);
        // Split by newlines and add <br /> between them
        const lines = textBefore.split("\n");
        lines.forEach((line, lineIdx) => {
          if (lineIdx > 0) {
            parts.push(<br key={`br-${index}-${lineIdx}`} />);
          }
          if (line) {
            parts.push(line);
          }
        });
      } else if (index === 0 && textWithPreservedNewlines.startsWith("\n")) {
        // Handle case where text starts with newline followed by emoji
        const leadingNewlines = textWithPreservedNewlines.match(/^(\n+)/)?.[1] || "";
        if (leadingNewlines) {
          // Add <br /> for each newline
          for (let i = 0; i < leadingNewlines.length; i++) {
            parts.push(<br key={`br-start-${i}`} />);
          }
          lastIndex = leadingNewlines.length;
          // Adjust the emoji index to account for the newlines we just added
          continue;
        }
      }

      // Add the emoji/avatar component
      const fullEmojiName = emojiMatch[0]; // Full match including colons (e.g., ":calendar:")
      parts.push(
        <SlackEmoji
          key={`emoji-${index}`}
          emoji={fullEmojiName}
          providerInstanceId={providerInstanceId}
          size={emojiSize}
          className="inline align-baseline mx-1"
        />
      );

      lastIndex = index + emojiMatch[0].length;
    }

    // Add remaining text after the last match
    // Convert newlines to <br /> elements so they render correctly
    if (lastIndex < textWithPreservedNewlines.length) {
      const remainingText = textWithPreservedNewlines.substring(lastIndex);
      const lines = remainingText.split("\n");
      lines.forEach((line, lineIdx) => {
        if (lineIdx > 0) {
          parts.push(<br key={`br-end-${lineIdx}`} />);
        }
        if (line) {
          parts.push(line);
        }
      });
    }

    // If no matches found, return the text with preserved newlines
    if (parts.length === 0) {
      return textWithPreservedNewlines;
    }

    return parts;
  }, [text, providerInstanceId, emojiSize, preview]);

  if (!parsedContent) {
    return null;
  }

  // If parsedContent is a string (no emojis found), render markdown directly
  if (typeof parsedContent === "string") {
    return (
      <div className={className}>
        <ReactMarkdown
          remarkPlugins={preview ? [remarkGfm] : [remarkGfm, remarkBreaks]}
          components={{
            // Make links open in browser
            a: ({ href, children, ...props }) => {
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
            // Style for code
            code: ({ className, ...props }) => {
              const isInline = !className?.includes("language-");
              if (isInline) {
                return (
                  <code
                    className="bg-muted px-1 py-0.5 rounded text-sm font-mono"
                    {...props}
                  />
                );
              }
              return (
                <code
                  className="block bg-muted p-2 rounded text-sm font-mono overflow-x-auto"
                  {...props}
                />
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
          {parsedContent}
        </ReactMarkdown>
      </div>
    );
  }

  // If we have emojis, we need to parse emojis in text parts and render markdown
  // Create a component that parses emojis in text nodes
  const TextWithEmojis = ({ text, keyPrefix, isPreview }: { text: string; keyPrefix: string; isPreview: boolean }) => {
    const emojiPattern = /:([a-zA-Z0-9_+-]+):/g;
    const parts: (string | ReactElement)[] = [];
    let lastIndex = 0;
    let match;

    while ((match = emojiPattern.exec(text)) !== null) {
      // Add text before the match (including any newlines)
      if (match.index > lastIndex) {
        parts.push(text.substring(lastIndex, match.index));
      }

      // Add the emoji component
      const fullEmojiName = match[0];
      parts.push(
        <SlackEmoji
          key={`${keyPrefix}-emoji-${match.index}`}
          emoji={fullEmojiName}
          providerInstanceId={providerInstanceId}
          size={emojiSize}
          className="inline align-baseline mx-1"
        />
      );

      lastIndex = match.index + match[0].length;
    }

    // Add remaining text after the last match
    if (lastIndex < text.length) {
      parts.push(text.substring(lastIndex));
    }
    
    // If the first part is an emoji (not a string), check if there are any newlines
    // before it in the original text that weren't captured
    if (parts.length > 0 && typeof parts[0] !== "string") {
      const textBeforeFirstEmoji = text.substring(0, text.search(emojiPattern));
      if (textBeforeFirstEmoji && textBeforeFirstEmoji.trim() === "") {
        // There's whitespace/newlines before the first emoji
        parts.unshift(textBeforeFirstEmoji);
      }
    }

    // If no emojis found, return the original text
    if (parts.length === 0) {
      return <>{text}</>;
    }

    // Render markdown on text parts, keep emoji components as-is
    return (
      <>
        {parts.map((part, idx) => {
          if (typeof part === "string") {
            return (
              <ReactMarkdown
                key={`${keyPrefix}-md-${idx}`}
                remarkPlugins={isPreview ? [remarkGfm] : [remarkGfm, remarkBreaks]}
                components={{
                  a: ({ href, children, ...props }) => {
                    if (isPreview) {
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
                          isPreview
                            ? ""
                            : isFromMe
                            ? "text-blue-100 hover:text-white hover:underline cursor-pointer inline"
                            : "text-blue-600 dark:text-blue-400 hover:text-blue-700 dark:hover:text-blue-300 hover:underline cursor-pointer inline"
                        }
                      >
                        {children}
                      </a>
                    );
                  },
                  strong: ({ ...props }) => (
                    <strong className="font-bold inline" {...props} />
                  ),
                  em: ({ ...props }) => (
                    <em className="italic inline" {...props} />
                  ),
                  code: ({ className, ...props }) => {
                    const isInline = !className?.includes("language-");
                    if (isInline) {
                      return (
                        <code
                          className="bg-muted px-1 py-0.5 rounded text-sm font-mono inline"
                          {...props}
                        />
                      );
                    }
                    return (
                      <code
                        className="block bg-muted p-2 rounded text-sm font-mono overflow-x-auto"
                        {...props}
                      />
                    );
                  },
                  p: ({ ...props }) => (
                    <span className="inline" {...props} />
                  ),
                  br: ({ ...props }) => {
                    if (isPreview) {
                      // In preview mode, replace line breaks with spaces to keep content on one line
                      return <span> </span>;
                    }
                    return <br {...props} />;
                  },
                  div: ({ ...props }) => <span className="inline" {...props} />,
                }}
              >
                {part}
              </ReactMarkdown>
            );
          }
          return part;
        })}
      </>
    );
  };

  return (
    <div className={className}>
      {parsedContent.map((part, index) => {
        if (typeof part === "string") {
          // Parse emojis in text parts and render markdown
          return <TextWithEmojis key={`text-${index}`} text={part} keyPrefix={`text-${index}`} isPreview={preview} />;
        }
        // Return emoji component as-is
        return part;
      })}
    </div>
  );
}
