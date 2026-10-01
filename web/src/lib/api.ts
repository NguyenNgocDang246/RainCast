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

const MESSAGES: Record<string, string> = {
  "link has no location": "Link này không chứa vị trí. Hãy mở địa điểm trong Google Maps rồi chia sẻ lại.",
  "address lookup failed": "Không tra được địa chỉ, thử lại sau ít giây.",
  "radar data is still loading; try again shortly": "Đang tải dữ liệu radar, thử lại sau ít giây.",
  "could not load radar data for this location": "Không tải được radar cho vị trí này.",
};

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, { cache: "no-store", signal });
  if (!res.ok) {
    const body = await res.json().catch(() => null);
    const msg: string = body?.error ?? `HTTP ${res.status}`;
    throw new Error(MESSAGES[msg] ?? msg);
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

/** Places matching a partial query, nearest to `near` first. */
export function suggest(query: string, near: Pick<Place, "lat" | "lon"> | null, signal: AbortSignal): Promise<Place[]> {
  const bias = near ? `&lat=${near.lat}&lon=${near.lon}` : "";
  return getJSON<Place[]>(`/api/suggest?q=${encodeURIComponent(query)}${bias}`, signal);
}
