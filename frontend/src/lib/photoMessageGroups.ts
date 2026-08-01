import { models } from "../../wailsjs/go/models";
import { timeToDate } from "./utils";

export interface PhotoMessageGroup {
  message: models.Message;
  messages: models.Message[];
}

interface AttachmentLike {
  type?: string;
  mimeType?: string;
  url?: string;
}

function hasPhotoCaption(message: models.Message): boolean {
  const body = message.body?.trim();
  if (!body) return false;

  // `isForwarded` is presentation metadata, not a photo caption. Current
  // providers leave it out of `body`, but tolerate legacy rows where Loom (or
  // an older provider) persisted the visual label as message text.
  if (message.isForwarded) {
    const normalized = body.toLocaleLowerCase().normalize("NFD").replace(/[\u0300-\u036f]/g, "");
    if (normalized === "forwarded" || normalized === "transfere") return false;
  }

  return true;
}

function isSinglePhotoMessage(message: models.Message): boolean {
  if (
    message.isFromMe ||
    hasPhotoCaption(message) ||
    message.isDeleted ||
    message.callType ||
    message.quotedMessageId
  ) return false;

  try {
    const attachments = JSON.parse(message.attachments || "") as AttachmentLike[];
    return Boolean(attachments.length === 1 && attachments[0]?.url && (
      attachments[0]?.type === "image" || attachments[0]?.mimeType?.startsWith("image/")
    ));
  } catch {
    return false;
  }
}

function senderKey(message: models.Message): string {
  return `${message.isFromMe ? "self" : "contact"}:${message.senderId || message.senderName || "unknown"}`;
}

function calendarDay(message: models.Message): string {
  const date = timeToDate(message.timestamp);
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
}

function isWithinFiveMinutes(previous: models.Message, current: models.Message): boolean {
  const gapMilliseconds = timeToDate(current.timestamp).getTime() - timeToDate(previous.timestamp).getTime();
  return gapMilliseconds >= 0 && gapMilliseconds <= 5 * 60 * 1000;
}

export function groupConsecutivePhotoMessages(messages: models.Message[]): PhotoMessageGroup[] {
  const groups: PhotoMessageGroup[] = [];

  for (const message of messages) {
    const previous = groups.at(-1);
    if (
      previous &&
      isSinglePhotoMessage(message) &&
      previous.messages.every(isSinglePhotoMessage) &&
      senderKey(previous.message) === senderKey(message) &&
      calendarDay(previous.message) === calendarDay(message) &&
      isWithinFiveMinutes(previous.messages.at(-1)!, message)
    ) {
      previous.messages.push(message);
    } else {
      groups.push({ message, messages: [message] });
    }
  }

  return groups;
}

export function mergePhotoGroupAttachments(messages: models.Message[]): string {
  return JSON.stringify(messages.flatMap((message) => {
    try {
      return JSON.parse(message.attachments || "") as AttachmentLike[];
    } catch {
      return [];
    }
  }));
}
