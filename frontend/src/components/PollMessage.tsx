import { useState } from "react";
import { Check, ChevronDown, ChevronUp } from "lucide-react";
import { VotePoll } from "../../wailsjs/go/main/App";
import { models } from "../../wailsjs/go/models";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

interface PollMessageProps {
  poll: models.Poll;
  conversationId: string;
  messageId: string;
  canVote: boolean;
  isFromMe: boolean;
  showToast: (message: string, type?: "error" | "success" | "info") => void;
}

export function PollMessage({ poll, conversationId, messageId, canVote, isFromMe, showToast }: PollMessageProps) {
  const { t } = useTranslation();
  const [submitting, setSubmitting] = useState(false);
  const [expandedOption, setExpandedOption] = useState<string | null>(null);
  const selected = poll.options.filter((option) => option.selected).map((option) => option.id);
  const voterCount = new Set(poll.options.flatMap((option) => option.voters?.map((voter) => voter.userId) ?? [])).size;
  const denominator = Math.max(poll.totalVoters, voterCount, ...poll.options.map((option) => option.votes), 1);

  const select = async (optionId: string) => {
    if (!canVote || poll.closed || submitting) return;
    const isSelected = selected.includes(optionId);
    const next = poll.maxSelections === 1
      ? (isSelected ? [] : [optionId])
      : (isSelected ? selected.filter((id) => id !== optionId) : [...selected, optionId]);
    if (next.length > poll.maxSelections) return;
    setSubmitting(true);
    try {
      await VotePoll(conversationId, messageId, next);
    } catch (error) {
      showToast(error instanceof Error ? error.message : t("poll_vote_error"), "error");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="min-w-64 max-w-sm space-y-3">
      <div>
        <div className="font-semibold leading-snug">{poll.question}</div>
        <div className={cn("text-xs mt-0.5", isFromMe ? "text-white/70" : "text-muted-foreground")}>
          {poll.maxSelections === 1 ? t("poll_select_one") : t("poll_select_multiple", { count: poll.maxSelections })}
        </div>
      </div>
      <div className="space-y-2">
        {poll.options.map((option) => {
		  const voters = option.voters ?? [];
          const percentage = Math.round((option.votes / denominator) * 100);
          const detailsOpen = expandedOption === option.id;
          return (
            <div key={option.id}>
              <button
                type="button"
                disabled={!canVote || poll.closed || submitting}
                onClick={() => select(option.id)}
                className={cn(
                  "relative isolate w-full overflow-hidden rounded-md border px-3 py-2 text-left transition-colors",
                  isFromMe ? "border-white/30 hover:bg-white/10" : "border-border hover:bg-background/60",
                  option.selected && (isFromMe ? "border-white/80" : "border-primary"),
                  (!canVote || poll.closed) && "cursor-default"
                )}
              >
                <span className={cn("absolute inset-y-0 left-0 -z-10", isFromMe ? "bg-white/15" : "bg-primary/15")} style={{ width: `${percentage}%` }} />
                <span className="flex items-center gap-2">
                  <span className={cn("flex size-4 shrink-0 items-center justify-center border", poll.maxSelections === 1 ? "rounded-full" : "rounded", option.selected && (isFromMe ? "bg-white text-blue-600" : "bg-primary text-primary-foreground"))}>
                    {option.selected && <Check className="size-3" />}
                  </span>
                  <span className="min-w-0 flex-1 text-sm">{option.text}</span>
                  <span className="text-xs tabular-nums">{option.votes} · {percentage}%</span>
                </span>
              </button>
              {poll.voterDetailsAvailable && voters.length > 0 && (
                <div>
                  <button type="button" className="mt-1 flex items-center gap-1 text-xs opacity-75 hover:opacity-100" onClick={() => setExpandedOption(detailsOpen ? null : option.id)}>
                    {t("poll_view_voters", { count: voters.length })}
                    {detailsOpen ? <ChevronUp className="size-3" /> : <ChevronDown className="size-3" />}
                  </button>
                  {detailsOpen && <div className="mt-1 space-y-0.5 pl-2 text-xs opacity-80">{voters.map((voter) => <div key={voter.userId}>{voter.displayName || voter.userId}</div>)}</div>}
                </div>
              )}
            </div>
          );
        })}
      </div>
      {poll.closed && <div className="text-xs italic opacity-70">{t("poll_closed")}</div>}
      {!canVote && !poll.closed && <div className="text-xs italic opacity-70">{t("poll_read_only")}</div>}
    </div>
  );
}
