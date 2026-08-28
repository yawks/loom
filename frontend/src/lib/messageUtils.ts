import type { models } from "../../wailsjs/go/models";
import { timeToDate } from "./utils";
import { getUserDisplayName } from "./userDisplayNames";

export const htmlFragmentToText = (text: string): string => {
  // Do not treat angle-bracket link markup such as <https://example.com|label> as HTML.
  if (!/<\/?(?:a|b|blockquote|br|div|em|i|li|ol|p|span|strong|u|ul)\b[^>]*>/i.test(text)) {
    return text;
  }
  const documentNode = new DOMParser().parseFromString(text, "text/html");
  documentNode.querySelectorAll("br").forEach((element) => element.replaceWith("\n"));
  documentNode.querySelectorAll("li").forEach((element) => {
    element.prepend("• ");
    element.append("\n");
  });
  documentNode.querySelectorAll("p, div, blockquote").forEach((element) => element.append("\n"));
  return documentNode.body.textContent?.replace(/\n{3,}/g, "\n\n").trim() ?? text;
};

export const getMessageDomId = (message: models.Message): string => {
  if (message.protocolMsgId?.trim()) return message.protocolMsgId;
  if (message.id) return `message-${message.id}`;
  return `ts-${timeToDate(message.timestamp).getTime()}`;
};

const comparableParticipantId = (userId?: string): string => (userId || "").trim().toLocaleLowerCase("en-US");

export const getQuotedSenderDisplayName = (
  message: models.Message,
  allMessages: models.Message[],
  participantNames: Map<string, string> | undefined,
  currentUserId: string | undefined,
  youLabel: string,
  contactLabel: string,
): string => {
  const explicitName = message.quotedSenderName?.trim();
  if (explicitName) return explicitName;

  const quotedId = message.quotedMessageId?.trim();
  const quotedMessage = quotedId
    ? allMessages.find((candidate) =>
        candidate.protocolMsgId === quotedId || getMessageDomId(candidate) === quotedId
      )
    : undefined;
  const quotedSenderId = message.quotedSenderId || quotedMessage?.senderId || "";
  const isCurrentUser = Boolean(
    quotedMessage?.isFromMe ||
    (quotedSenderId && currentUserId &&
      comparableParticipantId(quotedSenderId) === comparableParticipantId(currentUserId))
  );
  if (isCurrentUser) return youLabel;

  const senderName = quotedMessage?.senderName?.trim();
  if (senderName) return senderName;

  if (quotedSenderId) {
    return getUserDisplayName(quotedSenderId, { participantNames, allMessages });
  }
  return contactLabel;
};

// Compatibility for older persisted messages whose canonical quoted* fields are
// absent but whose body starts with Loom's serialized quoted-reply block.
// This intentionally detects the generic serialization format, not a provider.
export const normalizeSerializedQuotedReply = (message: models.Message): models.Message => {
  if (!message.body) return message;

  const lines = message.body
    .replace(/&gt;|&#(?:0*62);/gi, ">")
    .replace(/\r\n/g, "\n")
    .replace(/^[\s\u200B]+/, "")
    .split("\n");
  // Accept both emphasis styles because remote rich-text converters may normalize them.
  const header = lines[0]?.match(/^>\s*(?:\*([^*]+)\*|_([^_]+)_|(.+?))\s*$/);
  if (!header) return message;

  const quoteLines: string[] = [];
  let index = 1;
  while (index < lines.length && /^>\s?/.test(lines[index])) {
    quoteLines.push(lines[index].replace(/^>\s?/, ""));
    index += 1;
  }

  if (quoteLines.length === 0) return message;
  const body = lines.slice(index).join("\n").trimStart();
  const quotedMessageId = message.quotedMessageId || `${getMessageDomId(message)}-quote`;

  return {
    ...message,
    body,
    quotedMessageId,
    quotedSenderName: (header[1] || header[2] || header[3]).trim(),
    quotedBody: quoteLines.join("\n"),
  } as models.Message;
};

export const isDifferentDay = (date1: Date, date2: Date | null): boolean => {
  if (!date2) return true;
  return (
    date1.getFullYear() !== date2.getFullYear() ||
    date1.getMonth() !== date2.getMonth() ||
    date1.getDate() !== date2.getDate()
  );
};

export const formatDateSeparator = (date: Date, t: (key: string) => string): string => {
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const messageDate = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  const yesterday = new Date(today);
  yesterday.setDate(yesterday.getDate() - 1);

  if (messageDate.getTime() === today.getTime()) return t("today");
  if (messageDate.getTime() === yesterday.getTime()) return t("yesterday");

  const dayNames = [t("sunday"), t("monday"), t("tuesday"), t("wednesday"), t("thursday"), t("friday"), t("saturday")];
  const monthNames = [t("january"), t("february"), t("march"), t("april"), t("may"), t("june"), t("july"), t("august"), t("september"), t("october"), t("november"), t("december")];

  const dayName = dayNames[date.getDay()];
  const day = date.getDate();
  const month = monthNames[date.getMonth()];
  const year = date.getFullYear();

  if (year !== now.getFullYear()) return `${dayName} ${day} ${month} ${year}`;
  return `${dayName} ${day} ${month}`;
};

export function getColorFromString(str: string | undefined | null): string {
  const safe = (str ?? "").toString();
  if (safe.length === 0) return "hsl(0, 0%, 50%)";
  let hash = 0;
  for (let i = 0; i < safe.length; i++) {
    hash = safe.charCodeAt(i) + ((hash << 2) - hash);
  }
  const hue = Math.abs(hash) % 360;
  return `hsl(${hue}, 70%, 50%)`;
}

export function getSenderDisplayName(
  senderName: string | undefined,
  senderId: string,
  isFromMe: boolean,
  t: (key: string) => string
): string {
  if (isFromMe) {
    // Use the real name when the backend resolved one; fall back to "you" for phone-number-only values
    if (senderName?.trim() && senderName !== senderId && /^\d+$/.exec(senderName) === null) return senderName;
    return t("you") || "You";
  }
  if (senderName?.trim() && senderName !== senderId) return senderName;

  return senderId
    .replace(/^user-/, "")
    .replace(/^[a-z]+-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}
