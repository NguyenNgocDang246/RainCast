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
  /** null when the API has no database. */
  counts: { frames: number; regions: number } | null;
  geocoding: {
    geoapify_enabled: boolean;
    cache_hits: number;
    autocomplete_ok: number;
    search_ok: number;
    reverse_ok: number;
    rate_limited: number;
    errors: number;
    tile_cache_hits: number;
    tile_fetched: number;
    tile_errors: number;
  };
};

export type Station = { id: string; name: string; lat: number; lon: number };

/** A frame cmd/collect recorded; time is unix seconds. */
export type Frame = { time: number; path: string };

export type BacktestResult = {
  /** Vietnamese label; build one from method/pairs/trend/baseline instead. */
  name: string;
  /** Missing in reports saved before these fields existed. */
  method?: string;
  pairs?: number;
  trend?: boolean;
  baseline?: boolean;
  leads: { lead_min: number; scores: Scores; mae_dbz: number | null; brier?: number | null }[];
  overall: Scores;
  /** 95% bootstrap intervals; the delta is against report.reference. */
  csi_ci?: [number, number];
  delta_csi?: number;
  delta_csi_ci?: [number, number];
  brier?: number;
  bss?: number;
  brier_cal?: number;
  auc?: number;
  ms_per_issue?: number;
};

export type BacktestGroup = { climate: string; regions: number; issues: number; events: number; results: BacktestResult[] };

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
  /** Missing in reports from before multi-region collection. */
  events?: number;
  new_issues?: number;
  blocks?: number;
  reference?: string;
  regions?: { name: string; climate: string; frames: number; skipped: number; issues: number; events: number; csi: (number | null)[] }[];
  groups?: BacktestGroup[] | null;
};

/** GET /api/admin/backtest: the report `go run ./cmd/backtest` last wrote. */
export type BacktestStatus = { report: BacktestReport | null };

/** Rain events below which the backtest cannot tell methods apart (backtest.MinEvents). */
export const MIN_EVENTS = 30;

async function call(path: string, init?: RequestInit): Promise<Response> {
  // Same origin: the admin page runs locally, next to the backend, through
  // the Next rewrite.
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
