import { useTranslation } from "react-i18next";

export function MessageUnreadDivider() {
  const { t } = useTranslation();
  return (
    <div
      className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-primary"
      role="separator"
      aria-label={t("new_messages_separator")}
    >
      <span className="h-px flex-1 bg-border" />
      {t("new_messages_separator")}
      <span className="h-px flex-1 bg-border" />
    </div>
  );
}
