import type { Forecast } from "./api";

/** A forecast as of a moment, with `m0` the minute of its series that is then. */
export type ForecastNow = Forecast & { m0: number };

/**
 * Re-derives a forecast's summary for `now`: the API sums it up from the
 * radar frame, which is 10–20 minutes old, so rain that has since arrived or
 * passed is judged from the series instead, as nowcast.Summarize does from
 * minute m0 on. Minutes stay counted from the frame.
 */
export function forecastAt(f: Forecast, now: number): ForecastNow {
  const last = f.series.at(-1)?.minute ?? 0;
  const m0 = Math.min(Math.max(0, Math.round((now - Date.parse(f.frame_time)) / 60_000)), last);
  let arrival = -1;
  let heavy = -1;
  let accum = 0;
  for (const p of f.series) {
    if (p.minute < m0) continue;
    // Like the API's total, the current minute is already falling.
    if (p.minute > m0) accum += p.mm ?? 0;
    if (arrival < 0 && p.dbz >= f.threshold_dbz) arrival = p.minute;
    if (heavy < 0 && p.dbz >= f.heavy_dbz) heavy = p.minute;
  }
  // A forecast from before the series carried mm keeps the API's total.
  const perMinute = f.series.some((p) => p.mm !== undefined);
  return {
    ...f,
    m0,
    arrival_min: arrival,
    heavy_arrival_min: heavy,
    raining_now: arrival === m0,
    heavy_now: heavy === m0,
    accum_mm: perMinute ? accum : f.accum_mm,
  };
}
