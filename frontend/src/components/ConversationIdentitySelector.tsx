import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { GetConversationIdentities, SetConversationIdentity } from "../../wailsjs/go/main/App";

export function ConversationIdentitySelector({ conversationId }: { conversationId: string }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const key = ["conversation-identities", conversationId];
  const { data, error, isPending, refetch } = useQuery({
    queryKey: key,
    queryFn: () => GetConversationIdentities(conversationId),
    staleTime: 15_000,
    refetchInterval: 30_000,
    retry: false,
  });
  // Discovery can also hydrate identity metadata on already stored messages.
  const identitySignature = JSON.stringify(data?.identities ?? []);
  useEffect(() => {
    if (identitySignature !== "[]") {
      void queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
    }
  }, [identitySignature, conversationId, queryClient]);
  const selection = useMutation({
    mutationKey: ["conversation-identity", conversationId],
    mutationFn: (id: string) => SetConversationIdentity(conversationId, id),
    onSuccess: async (_, id) => {
      queryClient.setQueryData(key, (previous: typeof data) => previous ? { ...previous, selectedIdentityId: id } : previous);
      await queryClient.invalidateQueries({ queryKey: key });
    },
  });
  const label = (id: string) => {
    const identity = data?.identities.find((item) => item.id === id);
    return identity ? [identity.label, identity.address].filter(Boolean).join(" · ") : t("identity_unknown");
  };
  const unavailable = data?.selectedIdentityId && !data.identities.some((item) => item.id === data.selectedIdentityId);
  return (
    <div className="px-4 py-2 text-xs text-muted-foreground flex flex-wrap items-center gap-2">
      <label className="flex items-center gap-2">
        {t("identity_send_from")}
        <select
          aria-label={t("identity_send_from")}
          className="rounded border bg-background px-2 py-1 text-foreground max-w-80"
          value={data?.selectedIdentityId ?? ""}
          disabled={isPending || !!error || selection.isPending}
          onChange={(event) => selection.mutate(event.target.value)}
        >
          <option value="">{isPending ? t("loading") : t("identity_remote_default", { identity: label(data?.defaultIdentityId ?? "") })}</option>
          {unavailable && <option value={data.selectedIdentityId}>{t("identity_unavailable")}</option>}
          {data?.identities.map((identity) => <option key={identity.id} value={identity.id}>{label(identity.id)}</option>)}
        </select>
      </label>
      {selection.isPending && <span role="status">{t("identity_saving")}</span>}
      {(error || selection.error) && <span role="alert">{t("identity_load_failed")} <button className="underline" onClick={() => { selection.reset(); void refetch(); }}>{t("retry")}</button></span>}
      {unavailable && <span role="alert">{t("identity_unavailable")}</span>}
    </div>
  );
}
