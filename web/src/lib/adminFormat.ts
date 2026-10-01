export const pct = (v: number | null | undefined) => (v == null ? "–" : `${Math.round(v * 100)}%`);

export const dateTime = (iso: string | null | undefined) =>
  iso
    ? new Date(iso).toLocaleString("vi-VN", { hour: "2-digit", minute: "2-digit", day: "2-digit", month: "2-digit" })
    : "–";

export const time = (iso: string | null | undefined) =>
  iso ? new Date(iso).toLocaleTimeString("vi-VN", { hour: "2-digit", minute: "2-digit" }) : "–";

/** "12 phút", or a dash for "none within the horizon" (-1). */
export const minutes = (m: number) => (m < 0 ? "–" : `${m} phút`);

/** Reflectivity, with no-echo values shown as a dash. */
export const dbz = (v: number | null | undefined) => (v == null || v <= -32 ? "–" : `${Math.round(v)}`);

export const ago = (iso: string | null | undefined, now: number) => {
  if (!iso) return "–";
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 90) return `${s} giây trước`;
  if (s < 5400) return `${Math.round(s / 60)} phút trước`;
  return `${Math.round(s / 3600)} giờ trước`;
};
