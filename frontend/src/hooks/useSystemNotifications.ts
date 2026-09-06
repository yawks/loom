import { useEffect } from "react";
import {
  CleanupNotifications,
  EventsOn,
  InitializeNotifications,
  IsNotificationAvailable,
  SendNotification,
} from "../../wailsjs/runtime/runtime";
import { useMessageReadStore } from "@/lib/messageReadStore";
import i18n from "@/i18n";

interface SystemNotificationPayload { id: string; title: string; body?: string; conversationId: string; messageId: string }

const isUnread = (notification: SystemNotificationPayload) =>
  useMessageReadStore.getState().readByConversation[notification.conversationId]?.[notification.messageId] !== true;

export function useSystemNotifications() {
  useEffect(() => {
    if (typeof window === "undefined" || !window.runtime) return;
    let active = true;
    let available = false;
    void InitializeNotifications()
      .then(() => IsNotificationAvailable())
      .then((value) => { available = value; })
      .catch((error) => console.error("Unable to initialize notifications", error));

    const unsubscribe = EventsOn("system-notification", (raw: string) => {
      if (!active || !available) return;
      try {
        const notification: SystemNotificationPayload = JSON.parse(raw);
        if (!isUnread(notification)) return;
        void SendNotification(notification).catch((error) =>
          console.error("Unable to send system notification", error));
      } catch (error) {
        console.error("Invalid system notification", error);
      }
    });
    const unsubscribeBatch = EventsOn("system-notification-batch", (raw: string) => {
      // new-messages-batch is emitted first. Let its queued store update run,
      // then retain only messages that the canonical read store marked unread.
      window.setTimeout(() => {
        if (!active || !available) return;
        try {
          const unread = (JSON.parse(raw) as SystemNotificationPayload[]).filter(isUnread);
          if (unread.length === 0) return;
          const oneConversation = unread.every((item) => item.conversationId === unread[0].conversationId);
          void SendNotification({
            id: `sync:${Date.now()}`,
            title: oneConversation ? unread[0].title : i18n.t("notifications_unread_summary_title"),
            body: i18n.t("notifications_unread_summary", { count: unread.length }),
          }).catch((error) => console.error("Unable to send sync notification", error));
        } catch (error) { console.error("Invalid sync notification", error); }
      }, 0);
    });
    return () => {
      active = false;
      unsubscribe?.();
      unsubscribeBatch?.();
      void CleanupNotifications();
    };
  }, []);
}
