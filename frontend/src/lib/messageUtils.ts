import type { models } from "../../wailsjs/go/models";
import { timeToDate } from "./utils";

export const getMessageDomId = (message: models.Message): string => {
  if (message.protocolMsgId?.trim()) return message.protocolMsgId;
  if (message.id) return `message-${message.id}`;
  return `ts-${timeToDate(message.timestamp).getTime()}`;
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
    hash = safe.charCodeAt(i) + ((hash << 5) - hash);
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
  if (senderName && senderName.trim() && senderName !== senderId) return senderName;
  if (isFromMe) return t("you") || "You";

  const whatsappMatch = senderId.match(/^(\d+)@s\.whatsapp\.net$/);
  if (whatsappMatch) {
    const phoneNumber = whatsappMatch[1];
    if (phoneNumber.startsWith("33") && phoneNumber.length >= 10) {
      const countryCode = phoneNumber.substring(0, 2);
      const rest = phoneNumber.substring(2);
      return `+${countryCode} ${rest.substring(0, 1)} ${rest.substring(1, 3)} ${rest.substring(3, 5)} ${rest.substring(5, 7)} ${rest.substring(7)}`;
    }
    const formatted = phoneNumber.replace(/(\d{2})(?=\d)/g, "$1 ");
    return `+${formatted}`;
  }

  return senderId
    .replace(/^user-/, "")
    .replace(/^whatsapp-/, "")
    .replace(/^[a-z]+-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}
