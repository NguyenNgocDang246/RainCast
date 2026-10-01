import { bcp47, messages, type Locale } from "./i18n";

export const pct = (v: number | null | undefined) => (v == null ? "–" : `${Math.round(v * 100)}%`);

export const dateTime = (iso: string | null | undefined, locale: Locale) =>
  iso
    ? new Date(iso).toLocaleString(bcp47(locale), { hour: "2-digit", minute: "2-digit", day: "2-digit", month: "2-digit" })
    : "–";

export const time = (iso: string | null | undefined, locale: Locale) =>
  iso ? new Date(iso).toLocaleTimeString(bcp47(locale), { hour: "2-digit", minute: "2-digit" }) : "–";

/** "12 min" in the given language, or a dash for "none within the horizon" (-1). */
export const minutes = (m: number, locale: Locale) => (m < 0 ? "–" : messages[locale].units.min(m));

/** Reflectivity, with no-echo values shown as a dash. */
export const dbz = (v: number | null | undefined) => (v == null || v <= -32 ? "–" : `${Math.round(v)}`);

export const ago = (iso: string | null | undefined, now: number, locale: Locale) => {
  if (!iso) return "–";
  const u = messages[locale].units;
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 90) return u.secondsAgo(s);
  if (s < 5400) return u.minutesAgo(Math.round(s / 60));
  return u.hoursAgo(Math.round(s / 3600));
};
