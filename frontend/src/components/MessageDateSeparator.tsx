import { formatDateSeparator } from "@/lib/messageUtils";
import { useTranslation } from "react-i18next";

interface MessageDateSeparatorProps {
  date: Date;
  className?: string;
}

export function MessageDateSeparator({ date, className }: MessageDateSeparatorProps) {
  const { t } = useTranslation();
  const label = formatDateSeparator(date, t);

  return (
    <div
      className={`flex items-center gap-2 text-xs font-medium text-muted-foreground my-4 ${className ?? ""}`}
      role="separator"
      aria-label={label}
    >
      <span className="h-px flex-1 bg-border" />
      <span className="px-2">{label}</span>
      <span className="h-px flex-1 bg-border" />
    </div>
  );
}
