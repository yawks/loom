import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Pencil, Plus, Regex, Trash2, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  CreateMessageWatchRule,
  DeleteMessageWatchRule,
  GetMessageWatchRules,
  UpdateMessageWatchRule,
} from "../../wailsjs/go/main/App";
import type { models } from "../../wailsjs/go/models";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

interface MessageWatchRulesProps {
  conversationId: string;
}

export function MessageWatchRules({ conversationId }: MessageWatchRulesProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [pattern, setPattern] = useState("");
  const [isRegex, setIsRegex] = useState(false);
  const [editing, setEditing] = useState<models.MessageWatchRule | null>(null);
  const [error, setError] = useState("");
  const queryKey = ["message-watch-rules", conversationId];
  const { data: rules = [] } = useQuery({
    queryKey,
    queryFn: () => GetMessageWatchRules(conversationId),
    enabled: Boolean(conversationId),
  });

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey }),
      queryClient.invalidateQueries({ queryKey: ["highlightedMessages"] }),
      queryClient.invalidateQueries({ queryKey: ["highlightedMessageRefs"] }),
    ]);
  };
  const mutation = useMutation({
    mutationFn: async () => {
      const trimmed = pattern.trim();
      if (!trimmed) throw new Error(t("watch_rule_required"));
      return editing
        ? UpdateMessageWatchRule(editing.id, trimmed, isRegex)
        : CreateMessageWatchRule(conversationId, trimmed, isRegex);
    },
    onSuccess: async () => {
      setPattern("");
      setIsRegex(false);
      setEditing(null);
      setError("");
      await refresh();
    },
    onError: (reason) => setError(String(reason).replace(/^Error:\s*/, "")),
  });
  const remove = useMutation({
    mutationFn: (ruleId: number) => DeleteMessageWatchRule(ruleId),
    onSuccess: refresh,
    onError: (reason) => setError(String(reason).replace(/^Error:\s*/, "")),
  });

  const cancelEdit = () => {
    setEditing(null);
    setPattern("");
    setIsRegex(false);
    setError("");
  };

  return (
    <section className="space-y-3 border-t pt-4">
      <div>
        <h4 className="text-sm font-semibold text-muted-foreground">{t("watch_rules_title")}</h4>
        <p className="mt-1 text-xs text-muted-foreground">{t("watch_rules_help")}</p>
      </div>
      <form
        className="space-y-2"
        onSubmit={(event) => {
          event.preventDefault();
          mutation.mutate();
        }}
      >
        <div className="flex gap-1.5">
          <Input
            value={pattern}
            onChange={(event) => { setPattern(event.target.value); setError(""); }}
            placeholder={t("watch_rule_placeholder")}
            aria-label={t("watch_rule_placeholder")}
            disabled={mutation.isPending}
          />
          <Button
            type="button"
            size="icon"
            variant={isRegex ? "secondary" : "ghost"}
            className={cn("shrink-0", !isRegex && "text-muted-foreground")}
            aria-pressed={isRegex}
            title={t("watch_rule_regex")}
            onClick={() => setIsRegex((value) => !value)}
          >
            <Regex className="h-4 w-4" />
          </Button>
          <Button type="submit" size="icon" className="shrink-0" disabled={!pattern.trim() || mutation.isPending} title={editing ? t("save") : t("add")}>
            {editing ? <Check className="h-4 w-4" /> : <Plus className="h-4 w-4" />}
          </Button>
          {editing && (
            <Button type="button" size="icon" variant="ghost" className="shrink-0" onClick={cancelEdit} title={t("cancel")}>
              <X className="h-4 w-4" />
            </Button>
          )}
        </div>
        {error && <p className="text-xs text-destructive" role="alert">{error}</p>}
      </form>
      {rules.length === 0 ? (
        <p className="text-xs text-muted-foreground">{t("watch_rules_empty")}</p>
      ) : (
        <ul className="space-y-1.5">
          {rules.map((rule) => (
            <li key={rule.id} className="flex items-center gap-2 rounded-md border bg-muted/20 px-2 py-1.5">
              {rule.isRegex && <Regex className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-label={t("watch_rule_regex")} />}
              <code className="min-w-0 flex-1 truncate text-xs" title={rule.pattern}>{rule.pattern}</code>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="h-7 w-7 shrink-0"
                title={t("edit")}
                onClick={() => { setEditing(rule); setPattern(rule.pattern); setIsRegex(rule.isRegex); setError(""); }}
              >
                <Pencil className="h-3.5 w-3.5" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="h-7 w-7 shrink-0 text-destructive hover:text-destructive"
                title={t("delete")}
                disabled={remove.isPending}
                onClick={() => remove.mutate(rule.id)}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
