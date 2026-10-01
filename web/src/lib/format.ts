import type { Forecast } from "./api";

const COMPASS = ["Bắc", "Đông Bắc", "Đông", "Đông Nam", "Nam", "Tây Nam", "Tây", "Tây Bắc"];

/** 8-point compass name for a bearing in degrees. */
export function compass(deg: number): string {
  return COMPASS[Math.round((((deg % 360) + 360) % 360) / 45) % 8];
}

/** First minute after the frame at which the echo drops below dbz, or -1. */
export function dropsBelow(f: Forecast, dbz: number): number {
  const p = f.series.find((s) => s.minute > 0 && s.dbz < dbz);
  return p ? p.minute : -1;
}

export function clock(ms: number): string {
  return new Date(ms).toLocaleTimeString("vi-VN", { hour: "2-digit", minute: "2-digit" });
}

/** Strongest predicted echo from minute `from` (inclusive) to the end. */
export function peakFrom(f: Forecast, from: number): number {
  return f.series.reduce((m, s) => (s.minute >= from ? Math.max(m, s.dbz) : m), -32);
}
