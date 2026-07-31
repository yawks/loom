import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Bar, BarChart as RechartsBarChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { AlertCircle, CalendarDays, ChartNoAxesCombined, ChevronDown, Clock3, MessageCircle, Send, Users } from "lucide-react";
import { useTranslation } from "react-i18next";

import { GetCommunicationStats } from "../../wailsjs/go/main/App";
import type { models } from "../../wailsjs/go/models";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ProtocolIcon } from "./ProtocolIcon";

type Period = "today" | "yesterday" | "thisWeek" | "lastWeek" | "thisMonth" | "lastMonth" | "custom";

const startOfDay = (date: Date) => { const value = new Date(date); value.setHours(0, 0, 0, 0); return value; };
const addDays = (date: Date, days: number) => { const value = new Date(date); value.setDate(value.getDate() + days); return value; };
const startOfWeek = (date: Date) => { const value = startOfDay(date); return addDays(value, -((value.getDay() + 6) % 7)); };
const startOfMonth = (date: Date) => new Date(date.getFullYear(), date.getMonth(), 1);
const inputDate = (date: Date) => `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
const parseInputDate = (value: string) => { const [year, month, day] = value.split("-").map(Number); return new Date(year, month - 1, day); };

function rangeFor(period: Period, customFrom: string, customTo: string) {
  const now = new Date(); const today = startOfDay(now);
  if (period === "today") return [today, now];
  if (period === "yesterday") return [addDays(today, -1), today];
  if (period === "thisWeek") { const from = startOfWeek(now); return [from, now]; }
  if (period === "lastWeek") { const to = startOfWeek(now); return [addDays(to, -7), to]; }
  if (period === "thisMonth") { const from = startOfMonth(now); return [from, now]; }
  if (period === "lastMonth") { const to = startOfMonth(now); return [new Date(to.getFullYear(), to.getMonth() - 1, 1), to]; }
  return [parseInputDate(customFrom), addDays(parseInputDate(customTo), 1)];
}

const formatDuration = (seconds: number) => {
  const hours = Math.floor(seconds / 3600); const minutes = Math.floor((seconds % 3600) / 60);
  return hours ? `${hours} h ${minutes} min` : `${minutes} min`;
};

function Delta({ current, previous }: { current: number; previous: number }) {
  if (!previous) return <span className="text-xs text-muted-foreground">—</span>;
  const value = Math.round(((current - previous) / previous) * 100);
  return <span className={`text-xs font-medium ${value >= 0 ? "text-emerald-600" : "text-rose-600"}`}>{value >= 0 ? "+" : ""}{value}%</span>;
}

export function CommunicationStatsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t, i18n } = useTranslation();
  const [period, setPeriod] = useState<Period>("thisWeek");
  const [customFrom, setCustomFrom] = useState(inputDate(addDays(new Date(), -7)));
  const [customTo, setCustomTo] = useState(inputDate(new Date()));
  const [visibleContacts, setVisibleContacts] = useState(10);
  const [from, to] = useMemo(() => rangeFor(period, customFrom, customTo), [period, customFrom, customTo]);
  const validRange = to > from;
  const query = useQuery<models.CommunicationStats>({ queryKey: ["communicationStats", from.toISOString(), to.toISOString()], queryFn: () => GetCommunicationStats(from, to), enabled: open && validRange });
  const stats = query.data;
  const options: Array<[Period, string]> = [["today", t("today")], ["yesterday", t("yesterday")], ["thisWeek", t("stats.this_week")], ["lastWeek", t("stats.last_week")], ["thisMonth", t("stats.this_month")], ["lastMonth", t("stats.last_month")], ["custom", t("stats.custom")]];
  const chartData = stats?.series.map(point => ({ ...point, label: new Intl.DateTimeFormat(i18n.language, { month: "short", day: "numeric", ...(stats.series.length <= 48 ? { hour: "2-digit" } : {}) }).format(new Date(point.timestamp as unknown as string)) })) ?? [];

  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="max-w-[1100px] w-[95vw] h-[90vh] overflow-y-auto p-0 gap-0">
      <DialogHeader className="sticky top-0 z-10 bg-background border-b p-5 pr-12">
        <DialogTitle className="flex items-center gap-2"><ChartNoAxesCombined className="h-5 w-5" />{t("stats.title")}</DialogTitle>
        <div className="flex flex-wrap gap-2 pt-3">
          {options.map(([value, label]) => <Button key={value} size="sm" variant={period === value ? "default" : "outline"} onClick={() => { setPeriod(value); setVisibleContacts(10); }}>{label}</Button>)}
          {period === "custom" && <div className="flex items-center gap-2"><Input aria-label={t("stats.from")} type="date" value={customFrom} onChange={event => setCustomFrom(event.target.value)} className="w-auto" /><span>→</span><Input aria-label={t("stats.to")} type="date" value={customTo} onChange={event => setCustomTo(event.target.value)} className="w-auto" /></div>}
        </div>
      </DialogHeader>
      <div className="p-5 space-y-6">
        {query.isLoading && <div className="h-64 grid place-items-center text-muted-foreground">{t("loading")}</div>}
        {query.isError && <div className="h-40 grid place-items-center text-destructive"><AlertCircle className="h-5 w-5" />{t("stats.error")}</div>}
        {stats && <>
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
            {([["total", t("stats.total"), MessageCircle], ["sent", t("stats.sent"), Send], ["received", t("stats.received"), Users]] as const).map(([key, label, Icon]) => <div key={key} className="rounded-xl border bg-card p-4"><div className="flex justify-between"><Icon className="h-4 w-4 text-muted-foreground" /><Delta current={stats.summary[key]} previous={stats.previousSummary[key]} /></div><div className="text-2xl font-semibold mt-2">{stats.summary[key].toLocaleString(i18n.language)}</div><div className="text-sm text-muted-foreground">{label}</div></div>)}
          </div>
          <section className="rounded-xl border p-4"><h3 className="font-semibold mb-4">{t("stats.activity")}</h3><div className="h-72"><ResponsiveContainer width="100%" height="100%"><RechartsBarChart data={chartData}><CartesianGrid strokeDasharray="3 3" vertical={false} /><XAxis dataKey="label" tick={{ fontSize: 11 }} minTickGap={24} /><YAxis allowDecimals={false} tick={{ fontSize: 11 }} /><Tooltip /><Legend /><Bar dataKey="sent" name={t("stats.sent")} stackId="messages" fill="#2563eb" radius={[3,3,0,0]} /><Bar dataKey="received" name={t("stats.received")} stackId="messages" fill="#8b5cf6" radius={[3,3,0,0]} /></RechartsBarChart></ResponsiveContainer></div></section>
          <section><h3 className="font-semibold mb-3">{t("stats.by_instance")}</h3><div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">{stats.instances.map(instance => <div key={instance.providerInstanceId} className="rounded-xl border p-4"><div className="flex items-center gap-2 mb-3"><ProtocolIcon protocol={instance.providerId} size={22} /><span className="font-medium truncate">{instance.instanceName}</span></div><div className="grid grid-cols-3 text-center text-sm"><div><b className="block text-lg">{instance.total}</b>{t("stats.total")}</div><div><b className="block text-lg">{instance.sent}</b>{t("stats.sent")}</div><div><b className="block text-lg">{instance.received}</b>{t("stats.received")}</div></div></div>)}</div></section>
          <section><h3 className="font-semibold mb-3">{t("stats.calls_by_instance")}</h3><div className="rounded-xl border overflow-x-auto"><table className="w-full text-sm"><thead className="bg-muted/50"><tr><th className="text-left p-3">{t("stats.instance")}</th><th className="text-right p-3">{t("stats.calls")}</th><th className="text-right p-3">{t("stats.duration")}</th><th className="text-right p-3">{t("stats.unknown_duration")}</th></tr></thead><tbody>{stats.instances.map(instance => <tr key={instance.providerInstanceId} className="border-t"><td className="p-3"><span className="flex items-center gap-2"><ProtocolIcon protocol={instance.providerId} size={18} />{instance.instanceName}</span></td><td className="p-3 text-right">{instance.callCount}</td><td className="p-3 text-right">{formatDuration(instance.callDurationSecs)}</td><td className="p-3 text-right">{instance.callsWithoutDuration || "—"}</td></tr>)}</tbody></table></div></section>
          <section><h3 className="font-semibold mb-3">{t("stats.by_contact")}</h3><div className="rounded-xl border overflow-x-auto"><table className="w-full text-sm"><thead className="bg-muted/50"><tr><th className="text-left p-3">{t("contact")}</th><th className="text-left p-3">{t("stats.instance")}</th><th className="text-right p-3">{t("stats.total")}</th><th className="text-right p-3">{t("stats.sent")}</th><th className="text-right p-3">{t("stats.received")}</th><th className="text-right p-3">{t("stats.duration")}</th></tr></thead><tbody>{stats.contacts.slice(0, visibleContacts).map(contact => <tr key={`${contact.metaContactId}:${contact.providerInstanceId}`} className="border-t"><td className="p-3"><span className="flex items-center gap-2"><Avatar className="h-8 w-8"><AvatarImage src={contact.avatarUrl} /><AvatarFallback>{contact.displayName.slice(0,2).toUpperCase()}</AvatarFallback></Avatar><span className="font-medium">{contact.displayName}</span></span></td><td className="p-3"><span className="flex items-center gap-2"><ProtocolIcon protocol={contact.providerId} size={18} />{contact.instanceName}</span></td><td className="p-3 text-right font-medium">{contact.total}</td><td className="p-3 text-right">{contact.sent}</td><td className="p-3 text-right">{contact.received}</td><td className="p-3 text-right">{formatDuration(contact.callDurationSecs)}{contact.callsWithoutDuration > 0 && <span title={t("stats.some_unknown_duration")} className="ml-1 text-amber-600">*</span>}</td></tr>)}</tbody></table></div>{visibleContacts < stats.contacts.length && <div className="text-center mt-3"><Button variant="outline" onClick={() => setVisibleContacts(value => value + 10)}><ChevronDown className="h-4 w-4 mr-1" />{t("show_more")}</Button></div>}</section>
          <p className="text-xs text-muted-foreground flex items-center gap-1"><CalendarDays className="h-3.5 w-3.5" />{from.toLocaleDateString(i18n.language)} – {new Date(to.getTime() - 1).toLocaleDateString(i18n.language)} <Clock3 className="h-3.5 w-3.5 ml-2" />{t("stats.previous_comparison")}</p>
        </>}
      </div>
    </DialogContent>
  </Dialog>;
}
