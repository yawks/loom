import { useEffect } from "react";
import {
  EventsOn,
  SendNotification,
} from "../../wailsjs/runtime/runtime";
import { useMessageReadStore } from "@/lib/messageReadStore";
import i18n from "@/i18n";
import { initializeSystemNotifications } from "@/lib/systemNotifications";
import { authorizeSystemNotifications } from "@/lib/systemNotifications";
import { GetConfiguredProviders, GetNotificationSettings } from "../../wailsjs/go/main/App";

interface SystemNotificationPayload { id: string; title: string; subtitle?: string; body?: string; conversationId: string; messageId: string; timestamp: string }

const isUnread = (notification: SystemNotificationPayload) => {
  const conversation = useMessageReadStore.getState().readByConversation[notification.conversationId];
  const knownState = conversation?.[notification.messageId];
  if (knownState !== undefined) return knownState === false;
  const lastReadSeconds = Number(conversation?._lastReadTS);
  const messageMillis = Date.parse(notification.timestamp);
  if (Number.isFinite(lastReadSeconds) && Number.isFinite(messageMillis)) {
    return messageMillis > lastReadSeconds * 1000;
  }
  return true;
};

export function useSystemNotifications() {
  useEffect(() => {
    if (typeof window === "undefined" || !window.runtime) return;
    let active = true;
    const pendingSyncNotifications = new Map<string, SystemNotificationPayload>();
    let syncSummaryTimer: ReturnType<typeof window.setTimeout> | undefined;
    const ready = initializeSystemNotifications();

    void Promise.all([GetNotificationSettings(""), GetConfiguredProviders()])
      .then(async ([globalRule, accounts]) => {
        const accountRules = await Promise.all(accounts.map((account) => GetNotificationSettings(account.instanceId)));
        const hasEnabledRule = globalRule.enabled || accountRules.some((rule) => !rule.useGlobal && rule.enabled);
        if (hasEnabledRule) await authorizeSystemNotifications();
      })
      .catch((error) => console.error("Unable to check notification settings", error));

    const sendWhenReady = async (notification: { id: string; title: string; subtitle?: string; body?: string }) => {
      if (!await ready || !active) return;
      await SendNotification(notification);
    };

    const unsubscribe = EventsOn("system-notification", (raw: string) => {
      if (!active) return;
      try {
        const notification: SystemNotificationPayload = JSON.parse(raw);
        if (!isUnread(notification)) return;
        void sendWhenReady(notification).catch((error) =>
          console.error("Unable to send system notification", error));
      } catch (error) {
        console.error("Invalid system notification", error);
      }
    });
    const unsubscribeBatch = EventsOn("system-notification-batch", (raw: string) => {
      if (!active) return;
      try {
        for (const notification of JSON.parse(raw) as SystemNotificationPayload[]) {
          pendingSyncNotifications.set(`${notification.conversationId}:${notification.messageId}`, notification);
        }
        if (syncSummaryTimer !== undefined) window.clearTimeout(syncSummaryTimer);
        // Read-state reconciliation is asynchronous. Wait for the sync burst
        // to settle before removing messages that Loom already knows as read.
        syncSummaryTimer = window.setTimeout(() => {
          syncSummaryTimer = undefined;
          if (!active) return;
          const unread = [...pendingSyncNotifications.values()].filter(isUnread);
          pendingSyncNotifications.clear();
          if (unread.length === 0) return;
          const oneConversation = unread.every((item) => item.conversationId === unread[0].conversationId);
          void sendWhenReady({
            id: `sync:${Date.now()}`,
            title: oneConversation ? unread[0].title : i18n.t("notifications_unread_summary_title"),
            body: i18n.t("notifications_unread_summary", { count: unread.length }),
          }).catch((error) => console.error("Unable to send sync notification", error));
        }, 1500);
      } catch (error) { console.error("Invalid sync notification", error); }
    });
    return () => {
      active = false;
      unsubscribe?.();
      unsubscribeBatch?.();
      if (syncSummaryTimer !== undefined) window.clearTimeout(syncSummaryTimer);
      pendingSyncNotifications.clear();
    };
  }, []);
}
