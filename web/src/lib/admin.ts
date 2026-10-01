import type { Forecast } from "./api";

// Types mirror the Go admin API (internal/server/admin.go, internal/store).

export type Scores = {
  n: number;
  hits: number;
  misses: number;
  false_alarms: number;
  correct_negatives: number;
  accuracy: number | null;
  pod: number | null;
  far: number | null;
  csi: number | null;
};

export type LeadStats = { lead_min: number; model: Scores; persistence: Scores; mae_dbz: number | null };

/** Model with vs without intensity trend, on the forecasts that recorded both. */
export type Comparison = {
  leads: { lead_min: number; model: Scores; trend: Scores; persistence: Scores }[];
  model: Scores;
  trend: Scores;
  persistence: Scores;
};

export type Accuracy = {
  leads: LeadStats[];
  model: Scores;
  persistence: Scores;
  shadow: Comparison | null;
  arrival: { n: number; mae_min: number | null; bias_min: number | null };
  issued: number;
  verified: number;
  generated_at: string;
};

export type Overview = {
  server_time: string;
  pipeline: {
    location: { lat: number; lon: number };
    stations: Station[];
    started: string;
    ticks: number;
    last_tick: string | null;
    last_error: string;
    frames_in_feed: number;
    latest_frame: string | null;
    cached_regions: number;
  };
  counts: { frames: number; stations: number; issues: number; forecasts: number; verified: number; lookups: number };
  geocoding: {
    locationiq_enabled: boolean;
    cache_hits: number;
    locationiq_ok: number;
    locationiq_rate_limited: number;
    locationiq_errors: number;
    photon_ok: number;
    photon_errors: number;
    nominatim_ok: number;
    nominatim_errors: number;
  };
};

export type LeadResult = {
  lead_min: number;
  pred_dbz: number;
  pred_rain: boolean;
  persist_rain: boolean;
  obs_dbz: number | null;
  obs_rain: boolean | null;
};

export type Station = { id: string; name: string; lat: number; lon: number };

export type Issue = {
  station: string;
  issued_at: string;
  arrival_min: number;
  raining_now: boolean;
  result: Forecast;
  leads: LeadResult[];
};

/** obs: dBZ observed at each station (by id). */
export type Frame = { time: string; path: string; obs: Record<string, number>; recorded_at: string };

export type Lookup = {
  id: number;
  at: string;
  lat: number;
  lon: number;
  frame_time: string;
  raining_now: boolean;
  arrival_min: number;
  heavy_now: boolean;
  heavy_arrival_min: number;
  speed_kmh: number;
  direction_deg: number;
};

export type BacktestResult = {
  name: string;
  leads: { lead_min: number; scores: Scores; mae_dbz: number | null }[];
  overall: Scores;
};

export type BacktestReport = {
  generated_at: string;
  duration: string;
  frames: number;
  skipped: number;
  issues: number;
  points: number;
  from: number;
  to: number;
  results: BacktestResult[] | null;
};

async function call(path: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(path, { cache: "no-store", ...init });
  if (!res.ok) {
    const body = await res.json().catch(() => null);
    throw new Error(body?.error ?? `HTTP ${res.status}`);
  }
  return res;
}

export const adminGet = async <T>(path: string): Promise<T> => (await call(path)).json();

export const adminPost = async <T>(path: string): Promise<T> => (await call(path, { method: "POST" })).json();

/** Fetches an image and returns an object URL for <img>. */
export const adminImage = async (path: string): Promise<string> => URL.createObjectURL(await (await call(path)).blob());
