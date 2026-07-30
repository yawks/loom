import { GetContactExchangeStats, GetContactProfile } from "../../wailsjs/go/main/App";
import { Activity, AtSign, BriefcaseBusiness, Building2, CalendarDays, Clock3, MapPin, MessageCircle, Paperclip, Phone, SmilePlus } from "lucide-react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { models } from "../../wailsjs/go/models";
import { Skeleton } from "@/components/ui/skeleton";
import { timeToDate } from "@/lib/utils";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";

interface ParticipantTarget {
  userId: string;
  displayName: string;
  avatarUrl?: string;
  status: string;
}

interface ContactProfileDialogProps {
  conversationId: string;
  participant: ParticipantTarget | null;
  onClose: () => void;
}

function formatDate(value: unknown, locale: string): string {
  if (!value) return "";
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" })
    .format(timeToDate(value));
}

function formatDuration(seconds: number | undefined, t: (key: string, options?: Record<string, unknown>) => string): string {
  if (seconds === undefined || seconds === null) return "";
  if (seconds < 60) return t("contact_profile.seconds", { count: seconds });
  if (seconds < 3600) return t("contact_profile.minutes", { count: Math.round(seconds / 60) });
  if (seconds < 86400) return t("contact_profile.hours", { count: Math.round(seconds / 3600) });
  return t("contact_profile.days", { count: Math.round(seconds / 86400) });
}

function InfoRow({ icon, label, value }: { icon: React.ReactNode; label: string; value?: string }) {
  if (!value) return null;
  return (
    <div className="flex items-start gap-3 text-sm">
      <span className="mt-0.5 text-muted-foreground">{icon}</span>
      <div className="min-w-0">
        <p className="text-xs text-muted-foreground">{label}</p>
        <p className="break-words">{value}</p>
      </div>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="rounded-lg border bg-muted/20 p-3">
      <p className="text-lg font-semibold">{value}</p>
      <p className="text-xs text-muted-foreground">{label}</p>
    </div>
  );
}

function PresenceBadge({
  presence,
  statusText,
  statusEmoji,
  t,
}: {
  presence?: string;
  statusText?: string;
  statusEmoji?: string;
  t: (key: string) => string;
}) {
  const normalizedPresence = presence || "offline";
  const colorMap: Record<string, string> = {
    online: "bg-green-500",
    meeting: "bg-blue-500",
    away: "bg-yellow-500",
    busy: "bg-red-500",
    dnd: "bg-red-500",
    holiday: "bg-purple-500",
    offline: "bg-gray-500",
  };
  const labelMap: Record<string, string> = {
    online: t("online"),
    meeting: t("meeting") || "In a meeting",
    away: t("away") || "Away",
    busy: t("busy") || "Busy",
    dnd: t("dnd") || "Do not disturb",
    holiday: t("holiday") || "Holiday",
    offline: t("offline"),
  };
  const presenceLabel = labelMap[normalizedPresence] || normalizedPresence;
  const customStatus = [statusEmoji, statusText].filter(Boolean).join(" ");

  return (
    <div className="mt-2 flex max-w-full items-center gap-2 rounded-full border bg-background/95 px-3 py-1 text-xs shadow-sm">
      <span className={`h-2 w-2 shrink-0 rounded-full ${colorMap[normalizedPresence] || "bg-gray-500"}`} />
      <span className="truncate">{customStatus || presenceLabel}</span>
      {customStatus && (
        <span className="shrink-0 text-muted-foreground">· {presenceLabel}</span>
      )}
    </div>
  );
}

export function ContactProfileDialog({ conversationId, participant, onClose }: ContactProfileDialogProps) {
  const { t, i18n } = useTranslation();
  const enabled = Boolean(participant && conversationId);
  const profileQuery = useQuery<models.ContactProfile>({
    queryKey: ["contact-profile", conversationId, participant?.userId],
    queryFn: () => GetContactProfile(conversationId, participant!.userId),
    enabled,
    staleTime: 10 * 60 * 1000,
  });
  const statsQuery = useQuery<models.ContactExchangeStats>({
    queryKey: ["contact-exchange-stats", conversationId, participant?.userId],
    queryFn: () => GetContactExchangeStats(conversationId, participant!.userId),
    enabled,
    staleTime: 5 * 60 * 1000,
  });

  const profile = profileQuery.data;
  const stats = statsQuery.data;
  const name = profile?.displayName || participant?.displayName || "";
  const avatar = profile?.avatarUrl || participant?.avatarUrl;
  return (
    <Dialog open={participant !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-3xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="sr-only">{t("contact_profile.title")}</DialogTitle>
        </DialogHeader>
        {participant && (
          <div className="space-y-6">
            <div className="flex flex-col items-center text-center">
              <Avatar className="h-32 w-32 border shadow-sm">
                <AvatarImage src={avatar} alt={name} className="object-cover" />
                <AvatarFallback className="text-3xl">
                  {name.substring(0, 2).toUpperCase()}
                </AvatarFallback>
              </Avatar>
              <PresenceBadge
                presence={participant.status || profile?.presence}
                statusText={profile?.statusText}
                statusEmoji={profile?.statusEmoji}
                t={t}
              />
              <h2 className="mt-3 text-xl font-semibold">{name}</h2>
              {profile?.protocol && (
                <p className="mt-1 text-sm text-muted-foreground">{profile.protocol}</p>
              )}
            </div>

            {profileQuery.isLoading ? (
              <div className="grid grid-cols-2 gap-3"><Skeleton className="h-12" /><Skeleton className="h-12" /></div>
            ) : profile && (
              <div className="grid gap-4 sm:grid-cols-2">
                <InfoRow icon={<Phone className="h-4 w-4" />} label={t("contact_profile.phone")} value={profile.phoneNumbers?.join(", ")} />
                <InfoRow icon={<AtSign className="h-4 w-4" />} label={t("contact_profile.email")} value={profile.emails?.join(", ")} />
                <InfoRow icon={<BriefcaseBusiness className="h-4 w-4" />} label={t("contact_profile.job_title")} value={profile.jobTitle} />
                <InfoRow icon={<Building2 className="h-4 w-4" />} label={t("contact_profile.company")} value={[profile.company, profile.department].filter(Boolean).join(" · ")} />
                <InfoRow icon={<MapPin className="h-4 w-4" />} label={t("contact_profile.address")} value={profile.address} />
                <InfoRow icon={<Clock3 className="h-4 w-4" />} label={t("contact_profile.last_seen")} value={profile.lastSeen ? formatDate(profile.lastSeen, i18n.language) : ""} />
                <InfoRow icon={<Activity className="h-4 w-4" />} label={t("contact_profile.activity")} value={profile.providerFields?.activity} />
              </div>
            )}

            <div>
              <h3 className="mb-3 text-sm font-semibold text-muted-foreground">{t("contact_profile.exchanges")}</h3>
              {statsQuery.isLoading ? (
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                  {Array.from({ length: 8 }).map((_, index) => <Skeleton key={index} className="h-20" />)}
                </div>
              ) : statsQuery.isError ? (
                <p className="text-sm text-destructive">{t("contact_profile.stats_error")}</p>
              ) : stats && (
                <>
                  <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                    <Stat label={t("contact_profile.total_messages")} value={stats.totalMessages} />
                    {!stats.isGroup && <Stat label={t("contact_profile.sent")} value={stats.sentMessages} />}
                    <Stat label={stats.isGroup ? t("contact_profile.participant_messages") : t("contact_profile.received")} value={stats.receivedMessages} />
                    <Stat label={t("contact_profile.active_days")} value={stats.activeDays} />
                    <Stat label={t("contact_profile.attachments")} value={stats.attachmentMessages} />
                    <Stat label={t("contact_profile.reactions_given")} value={stats.reactionsGiven} />
                    <Stat label={t("contact_profile.reactions_received")} value={stats.reactionsReceived} />
                    <Stat label={t("contact_profile.calls")} value={stats.calls} />
                  </div>
                  <div className="mt-4 grid gap-3 sm:grid-cols-2">
                    <InfoRow icon={<CalendarDays className="h-4 w-4" />} label={t("contact_profile.first_exchange")} value={formatDate(stats.firstExchange, i18n.language)} />
                    <InfoRow icon={<MessageCircle className="h-4 w-4" />} label={t("contact_profile.last_exchange")} value={formatDate(stats.lastExchange, i18n.language)} />
                    {!stats.isGroup && <InfoRow icon={<SmilePlus className="h-4 w-4" />} label={t("contact_profile.contact_response")} value={formatDuration(stats.medianContactResponseSecs, t)} />}
                    {!stats.isGroup && <InfoRow icon={<Paperclip className="h-4 w-4" />} label={t("contact_profile.my_response")} value={formatDuration(stats.medianMyResponseSecs, t)} />}
                  </div>
                </>
              )}
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
