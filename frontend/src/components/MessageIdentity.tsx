import { useTranslation } from "react-i18next";
import type { models } from "../../wailsjs/go/models";

export function MessageIdentity({ message }: { message: models.Message }) {
  const { t } = useTranslation();
  if (!message.localIdentityApplicable) return null;
  const identity = message.localIdentityId
    ? [message.localIdentityLabel, message.localIdentityAddress].filter(Boolean).join(" · ") || t("identity_unknown")
    : t("identity_unknown");
  return <span className="block border-t pt-2 text-xs text-muted-foreground" title={identity}>
    {t(message.isFromMe ? "identity_sent_from" : "identity_received_on", { identity })}
  </span>;
}
