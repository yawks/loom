import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { OpenConversation } from "../../wailsjs/go/main/App";
import { useMemo, memo } from "react";
import ReactMarkdown, { defaultUrlTransform, type Components } from "react-markdown";
import { Emoji } from "./Emoji";
import { CodeBlock } from "./CodeBlock";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import { annotateUnlabeledCodeFences, transformUrls, fixCodeBlocks } from "../lib/utils";
import { cleanEmoji } from "@/lib/userDisplayNames";
import { cn } from "@/lib/utils";
import { useRenderCount } from "@/hooks/useRenderCount";
import { useAppStore } from "@/lib/store";
import { htmlFragmentToText } from "@/lib/messageUtils";
import { rehypeCanonicalBreaks } from "../lib/markdownBreaks";
import { rehypeCanonicalUnderline } from "../lib/markdownUnderline";
import { rehypeCanonicalStyle } from "../lib/markdownStyle";
import { outsideMarkdownCode, repairLegacyCodeFences } from "../lib/markdownCode";
import { emojiShortcodePattern } from "../lib/emojiShortcodes";
import type { PluggableList } from "unified";
import type { models } from "../../wailsjs/go/models";

interface SerializedInlineQuote {
  sender: string;
  quotedText: string;
  body: string;
}

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

function buildComponents(isFromMe: boolean, preview: boolean, isInline: boolean, providerInstanceId?: string, emojiSize = 16, multilinePreview = false): Components {
  return {
    a: ({ href, children, ...props }) => {
      if (href?.startsWith("loom://mention")) {
        return (
          <button
            type="button"
            onClick={(event) => {
              event.preventDefault();
              event.stopPropagation();
              const userId = new URL(href).searchParams.get("userId");
              if (!userId || !providerInstanceId) return;

              const { metaContacts, setSelectedContact, setSelectedProviderFilter } = useAppStore.getState();
              const contact = metaContacts.find((candidate) =>
                candidate.linkedAccounts.some((account) =>
                  account.providerInstanceId === providerInstanceId &&
                  (account.userId === userId || account.conversationId === userId)
                )
              );
              if (contact) {
                const account = contact.linkedAccounts.find((candidate) =>
                  candidate.providerInstanceId === providerInstanceId &&
                  (candidate.userId === userId || candidate.conversationId === userId)
                );
                setSelectedProviderFilter(providerInstanceId);
                setSelectedContact(account && contact.linkedAccounts[0] !== account
                  ? { ...contact, linkedAccounts: [account, ...contact.linkedAccounts.filter((candidate) => candidate !== account)] } as models.MetaContact
                  : contact);
                return;
              }

              void OpenConversation({
                providerInstanceId,
                participantIds: [userId],
                conversationType: "direct",
                title: "",
              }).then((resolution) => {
                const resolved = resolution.created ?? resolution.matches?.[0];
                if (!resolved) return;
                setSelectedProviderFilter(providerInstanceId);
                setSelectedContact(resolved);
              }).catch((error) => console.error("Failed to open mentioned participant conversation:", error));
            }}
            className={cn(
              "inline-flex cursor-pointer whitespace-nowrap rounded-md border px-1.5 py-0.5 font-medium leading-none transition-colors",
              isFromMe
                ? "border-white/35 bg-white/15 text-inherit hover:bg-white/25"
                : "border-primary/25 bg-primary/10 text-primary hover:bg-primary/20"
            )}
          >
            {children}
          </button>
        );
      }
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
      // Message previews are text summaries. Rich cards can contain Markdown
      // images with large intrinsic dimensions; keeping those images inside a
      // line-clamped snippet makes the line box retain that height and leaves a
      // large blank area in compact lists. Preserve useful alternative text,
      // but never render the media itself in preview mode.
      if (preview) return alt ? <span>{alt}</span> : null;
      return <img src={src} alt={alt ?? ""} {...props} />;
    },
    p: ({ className, ...props }) => (
      isInline
        ? <span className={className} {...props} />
        : <p className={cn("m-0", (!preview || multilinePreview) && "[&+p]:mt-[1.5em]", className)} {...props} />
    ),
    br: ({ ...props }) => (preview && !multilinePreview ? <span> </span> : <br {...props} />),
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
  preview?: boolean; // If true, render as preview (no blue links, single line by default)
  multilinePreview?: boolean; // Preserve line breaks and paragraphs in previews
  isFromMe?: boolean; // If true, message is from current user
  highlightQuery?: string; // Literal text to emphasize in search previews
  mentions?: models.MessageMention[];
}

function mergeMentionFragments(text: string, mentions: models.MessageMention[]): models.MessageMention[] {
  const ordered = [...mentions].sort((left, right) => left.start - right.start);
  return ordered.reduce<models.MessageMention[]>((merged, mention) => {
    const previous = merged.at(-1);
    if (!previous) return [{ ...mention }];
    const previousEnd = previous.start + previous.length;
    const gap = text.slice(previousEnd, mention.start);
    if (previous.userId === mention.userId && mention.start >= previousEnd && /^\s*$/.test(gap)) {
      previous.length = mention.start + mention.length - previous.start;
      previous.displayName = `${previous.displayName} ${mention.displayName}`.trim();
      return merged;
    }
    merged.push({ ...mention });
    return merged;
  }, []);
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
  multilinePreview = false,
  isFromMe = false,
  highlightQuery = "",
  mentions = [],
}: MessageTextProps) {
  useRenderCount("MessageText", { textLength: text?.length, preview });
  const serializedInlineQuote = useMemo(() => parseSerializedInlineQuote(text), [text]);

  const parsedContent = useMemo(() => {
    if (!text) return null;

    // Preprocessing
    // Preserve Loom's explicit underline extension while stripping provider
    // HTML. Standard Markdown intentionally has no underline syntax.
    const richTags: string[] = [];
    let canonicalText = text;
    if (!preview && mentions.length > 0) {
      const ordered = mergeMentionFragments(text, mentions).sort((left, right) => right.start - left.start);
      for (const mention of ordered) {
        const end = mention.start + mention.length;
        if (mention.start < 0 || mention.length <= 0 || end > canonicalText.length) continue;
        const visible = canonicalText.slice(mention.start, end);
        const markdownLabel = visible.replace(/([\\[\]])/g, "\\$1");
        canonicalText = canonicalText.slice(0, mention.start)
          + `[${markdownLabel}](loom://mention?userId=${encodeURIComponent(mention.userId)})`
          + canonicalText.slice(end);
      }
    }
    let processedText = outsideMarkdownCode(repairLegacyCodeFences(canonicalText), (prose) => {
      const richProtected = prose.replace(/<br\s*\/?\s*>|<\/?loom-style\b[^>]*>/gi, (tag) => {
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
      return processedText;
    });
    processedText = annotateUnlabeledCodeFences(processedText);
    processedText = outsideMarkdownCode(processedText, (prose) => cleanEmoji(prose).replace(
      emojiShortcodePattern(),
      (match, name, offset, source) => {
        const before = source.slice(0, offset);
        const currentLine = before.slice(before.lastIndexOf("\n") + 1);
        if (/https?:\/\/\S*$/.test(currentLine)) return match;
        return `![${match}](loom-emoji://${encodeURIComponent(name)})`;
      },
    ));
    if (preview && !multilinePreview) processedText = processedText.replace(/\n+/g, " ");
    return processedText;
  }, [text, preview, multilinePreview, mentions]);

  const blockComponents = useMemo(
    () => buildComponents(isFromMe, preview, false, providerInstanceId, emojiSize, multilinePreview),
    [isFromMe, preview, providerInstanceId, emojiSize, multilinePreview]
  );
  const inlineComponents = useMemo(
    () => buildComponents(isFromMe, preview, true, providerInstanceId, emojiSize, multilinePreview),
    [isFromMe, preview, providerInstanceId, emojiSize, multilinePreview]
  );

  const remarkPlugins = useMemo(
    () => (preview && !multilinePreview ? [remarkGfm] : [remarkGfm, remarkBreaks]),
    [preview, multilinePreview]
  );
  const rehypePlugins = useMemo<PluggableList>(
    () => [
      rehypeCanonicalBreaks,
      rehypeCanonicalUnderline,
      rehypeCanonicalStyle,
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

  const renderMarkdown = renderMarkdownBase;

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

  return (
    <div className={cn(className, "max-w-full overflow-hidden")}>
      {renderMarkdown(parsedContent, preview && !multilinePreview)}
    </div>
  );
});
