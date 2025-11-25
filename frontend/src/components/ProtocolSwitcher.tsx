import { Button } from "@/components/ui/button";
import type { models } from "../../wailsjs/go/models";
import { ProtocolIcon } from "./ProtocolIcon";

export function ProtocolSwitcher({
  linkedAccounts,
}: {
  linkedAccounts: models.LinkedAccount[];
}) {
  if (linkedAccounts.length <= 1) {
    return null;
  }

  return (
    <div className="flex space-x-2">
      {linkedAccounts.map((account) => (
        <Button key={account.id} variant="outline" size="sm" className="gap-2">
          <ProtocolIcon protocol={account.protocol} size={16} />
        </Button>
      ))}
    </div>
  );
}
