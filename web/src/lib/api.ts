// Mirrors the JSON of the Go backend (internal/pipeline, internal/geocode).
export type Forecast = {
  location: { lat: number; lon: number };
  frame_time: string;
  threshold_dbz: number;
  /** Below this, rain is only "possible" (very light echoes may not reach the ground). */
  likely_dbz: number;
  heavy_dbz: number;
  /** Predicted reflectivity over the target, minute by minute from frame_time. */
  series: { minute: number; dbz: number }[];
  raining_now: boolean;
  /** First minute (from frame_time) with rain, or -1 if none within 60 minutes. */
  arrival_min: number;
  heavy_now: boolean;
  heavy_arrival_min: number;
  speed_kmh: number;
  /** Where the rain is heading, clockwise from north. */
  direction_deg: number;
  motion_reliable: boolean;
};

export type Place = { name: string; address: string; lat: number; lon: number };

/**
 * Prefix for every /api request. Empty means same origin: in production Caddy
 * routes /api to the Go backend, in dev Next rewrites it (next.config.ts).
 */
export const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "";

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(API_BASE + path, { cache: "no-store", signal });
  if (!res.ok) {
    const body = await res.json().catch(() => null);
    // Kept in the backend's words; components translate with errorText.
    throw new Error(body?.error ?? `HTTP ${res.status}`);
  }
  return res.json() as Promise<T>;
}

/** Forecast for a chosen place. */
export function fetchForecast(place: Pick<Place, "lat" | "lon">): Promise<Forecast> {
  return getJSON<Forecast>(`/api/forecast?lat=${place.lat}&lon=${place.lon}`);
}

/** Resolves an address, "lat, lon" or a Google Maps link. */
export function geocode(query: string): Promise<Place[]> {
  return getJSON<Place[]>(`/api/geocode?q=${encodeURIComponent(query)}`);
}

/** Names a point picked on the map; the point itself is kept. */
export function reverse(lat: number, lon: number, signal?: AbortSignal): Promise<Place> {
  return getJSON<Place>(`/api/reverse?lat=${lat}&lon=${lon}`, signal);
}

/** Places matching a partial query, nearest to `near` first. */
export function suggest(query: string, near: Pick<Place, "lat" | "lon"> | null, signal: AbortSignal): Promise<Place[]> {
  const bias = near ? `&lat=${near.lat}&lon=${near.lon}` : "";
  return getJSON<Place[]>(`/api/suggest?q=${encodeURIComponent(query)}${bias}`, signal);
}
