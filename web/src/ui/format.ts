const dateOnly = /^\d{4}-\d{2}-\d{2}$/;

function format(value: string | null | undefined, timeZone: string, withTime: boolean): string {
  if (!value) return "—";
  // A calendar date has no instant; formatting it in UTC keeps the day unchanged.
  const zone = dateOnly.test(value) ? "UTC" : timeZone;
  const options: Intl.DateTimeFormatOptions = { day: "numeric", month: "short", year: "numeric", timeZone: zone };
  if (withTime) Object.assign(options, { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
  return new Intl.DateTimeFormat("tr-TR", options).format(new Date(value)).replace(",", "");
}

export const formatDate = (value: string | null | undefined, timeZone: string) => format(value, timeZone, false);
export const formatDateTime = (value: string | null | undefined, timeZone: string) => format(value, timeZone, true);

/** Today's calendar date (YYYY-MM-DD) in the given timezone. */
export const todayIn = (timeZone: string, now = new Date()) => new Intl.DateTimeFormat("en-CA", { timeZone }).format(now);

const dayNumber = (date: string) => Date.UTC(+date.slice(0, 4), +date.slice(5, 7) - 1, +date.slice(8, 10)) / 864e5;

/** Whole days from today (company timezone) until a calendar date; 0 = today. */
export const daysUntil = (date: string, timeZone: string, now = new Date()) => dayNumber(date) - dayNumber(todayIn(timeZone, now));

export function relativeTime(value: string, now = new Date()) {
  const d = now.getTime() - new Date(value).getTime(), a = Math.abs(d);
  if (a < 6e4) return "az önce";
  const r = a < 36e5 ? `${Math.round(a / 6e4)} dk` : a < 864e5 ? `${Math.round(a / 36e5)} saat` : `${Math.round(a / 864e5)} gün`;
  return d < 0 ? `${r} sonra` : `${r} önce`;
}

/** A calendar date n days after `date` (both YYYY-MM-DD). */
export const addDays = (date: string, n: number) => new Date(Date.UTC(+date.slice(0, 4), +date.slice(5, 7) - 1, +date.slice(8, 10) + n)).toISOString().slice(0, 10);

export function sinceText(value: string, now = new Date()) {
  const d = now.getTime() - new Date(value).getTime();
  return d < 36e5 ? `${Math.max(1, Math.round(d / 6e4))} dakikadır` : d < 864e5 ? `${Math.round(d / 36e5)} saattir` : `${Math.floor(d / 864e5)} gündür`;
}

/** The instant at which a wall clock in `timeZone` shows `date` `time` (YYYY-MM-DD, HH:MM), as RFC 3339. */
export function zonedInstant(date: string, time: string, timeZone: string) {
  const asUtc = Date.parse(`${date}T${time}:00Z`);
  // The zone's offset at that moment: how its wall clock differs from UTC.
  const parts = Object.fromEntries(new Intl.DateTimeFormat("en-US", { timeZone, hourCycle: "h23", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })
    .formatToParts(new Date(asUtc)).map((p) => [p.type, p.value]));
  const wall = Date.parse(`${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}:00Z`);
  return new Date(asUtc - (wall - asUtc)).toISOString();
}
