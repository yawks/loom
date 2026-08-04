import { useTranslation } from "react-i18next";

export function MessageUnreadDivider({ count }: { count: number }) {
  const { t } = useTranslation();
  const label = t("unread_separator", { count });

  return (
    <div
      className="my-3 flex items-center gap-3 text-xs font-semibold uppercase tracking-wide text-orange-500 dark:text-orange-400"
      role="separator"
      aria-label={label}
    >
      <span className="h-px flex-1 bg-primary/70" />
      <span>{label}</span>
      <span className="h-px flex-1 bg-primary/70" />
    </div>
  );
}
