import { Button } from "@/components/ui/button";
import { Info, MessageSquare } from "lucide-react";
import { ProtocolSwitcher } from "./ProtocolSwitcher";
import type { models } from "../../wailsjs/go/models";
import { useTranslation } from "react-i18next";

export function MessageHeader({
  displayName,
  linkedAccounts,
  onToggleThreads,
  onToggleDetails,
}: {
  displayName: string;
  linkedAccounts: models.LinkedAccount[];
  onToggleThreads: () => void;
  onToggleDetails: () => void;
}) {
  const { t } = useTranslation();

  return (
    <div className="p-4 border-b flex justify-between items-center shrink-0">
      <h2 className="text-lg font-semibold">{displayName}</h2>
      <div className="flex items-center gap-2">
        <Button
          variant="ghost"
          size="icon"
          onClick={onToggleDetails}
          title="Conversation Details"
        >
          <Info className="h-4 w-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          onClick={onToggleThreads}
          title={t("threads")}
        >
          <MessageSquare className="h-4 w-4" />
        </Button>
        <ProtocolSwitcher linkedAccounts={linkedAccounts} />
      </div>
    </div>
  );
}

