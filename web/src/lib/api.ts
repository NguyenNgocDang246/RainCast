// Mirrors the JSON of the Go backend (internal/pipeline, internal/geocode).
export type Forecast = {
  location: { lat: number; lon: number };
  frame_time: string;
  /** When the next radar frame is expected: no newer forecast before then. */
  next_due?: string;
  threshold_dbz: number;
  /** Below this, rain is only "possible" (very light echoes may not reach the ground). */
  likely_dbz: number;
  heavy_dbz: number;
  /**
   * Predicted reflectivity over the target, minute by minute from frame_time,
   * with the rain falling in that minute (absent when none).
   */
  series: { minute: number; dbz: number; mm?: number }[];
  raining_now: boolean;
  /** First minute (from frame_time) with rain, or -1 if none within 60 minutes. */
  arrival_min: number;
  heavy_now: boolean;
  heavy_arrival_min: number;
  speed_kmh: number;
  /** Where the rain is heading, clockwise from north. */
  direction_deg: number;
  motion_reliable: boolean;
  /** How much the ensemble's members agree on the motion: |mean| / mean speed, 0–1. */
  motion_coherence?: number;
  /**
   * How well the motion explains the last radar change: near 1 it is how the
   * rain moved, 0 or below no better than standing still. Absent when it
   * cannot tell.
   */
  motion_gain?: number;
  /** Each motion method's own motion. */
  motion_members?: { method: string; speed_kmh: number; direction_deg: number }[];
  /** Rain expected over the 60 minutes after frame_time, in mm (estimated from radar). */
  accum_mm?: number;
  /** Storm cells within 15 km that are forming or strengthening. */
  storms_nearby?: number;
};

export type Place = { name: string; address: string; lat: number; lon: number };

/**
 * Prefix for public /api requests. Empty means same origin: the Next
 * rewrite (next.config.ts) routes /api to the Go backend. On Vercel it is
 * the backend's own URL, so the backend sees the visitor's IP and country
 * and its CDN caches map tiles.
 */
export const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "";

/** The error of a request that got no answer at all: offline, blocked or cut off. */
export const NETWORK_ERROR = "network error";

/** fetch, with a failure to reach the server thrown as NETWORK_ERROR; an abort stays an abort. */
async function request(url: string, init?: RequestInit): Promise<Response> {
  try {
    return await fetch(url, init);
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw e;
    throw new Error(NETWORK_ERROR);
  }
}

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  return readJSON<T>(await request(API_BASE + path, { cache: "no-store", signal }));
}

async function readJSON<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const body = await res.json().catch(() => null);
    // Kept in the backend's words (or the status, from a proxy's error
    // page); components translate with userError or errorText.
    throw new Error(body?.error ?? `HTTP ${res.status}`);
  }
  return res.json() as Promise<T>;
}

/** Radar tiles a forecast reads, which the browser downloads itself (internal/pipeline TilePlan). */
type TilePlan = { frame: number; tiles: { time: number; x: number; y: number; url: string }[] };
type TilesNeeded = { tiles_needed: TilePlan };

const needsTiles = (r: Forecast | TilesNeeded): r is TilesNeeded => "tiles_needed" in r;

async function sha256Hex(data: BufferSource): Promise<string> {
  const sum = new Uint8Array(await crypto.subtle.digest("SHA-256", data));
  return Array.from(sum, (b) => b.toString(16).padStart(2, "0")).join("");
}

/** The tiles' content hash, as pipeline.ContentHash computes it. */
async function contentHash(plan: TilePlan, tiles: Uint8Array<ArrayBuffer>[]): Promise<string> {
  const lines = await Promise.all(
    plan.tiles.map(async (t, i) => `${t.time}:${t.x}:${t.y}:${await sha256Hex(tiles[i])}\n`),
  );
  return sha256Hex(new TextEncoder().encode(lines.join("")));
}

/**
 * Forecast for a chosen place. A backend on a shared host (Vercel) answers
 * with the radar tiles to download instead: the browser fetches them from
 * RainViewer (its own per-IP limit, its own HTTP cache), asks whether the
 * server already has a forecast from exactly those tiles, and uploads them
 * when it does not.
 */
export async function fetchForecast(place: Pick<Place, "lat" | "lon">): Promise<Forecast> {
  const path = `/api/forecast?lat=${place.lat}&lon=${place.lon}`;
  let answer = await getJSON<Forecast | TilesNeeded>(path);
  for (let attempt = 0; ; attempt++) {
    if (!needsTiles(answer)) return answer;
    const plan = answer.tiles_needed;
    const tiles = await Promise.all(
      plan.tiles.map(async (t) => {
        const res = await request(t.url, { cache: "force-cache" });
        if (!res.ok) throw new Error(`radar tile: HTTP ${res.status}`);
        return new Uint8Array(await res.arrayBuffer());
      }),
    );
    const cached = await getJSON<Forecast | TilesNeeded>(`${path}&h=${await contentHash(plan, tiles)}`);
    if (!needsTiles(cached)) return cached;

    const form = new FormData();
    plan.tiles.forEach((t, i) => form.append(`${t.time}_${t.x}_${t.y}`, new Blob([tiles[i]], { type: "image/png" }), "tile.png"));
    const res = await request(API_BASE + path, { method: "POST", body: form, cache: "no-store" });
    // A new radar frame arrived meanwhile: the answer carries the new plan.
    if (res.status === 409 && attempt === 0) {
      answer = (await res.json()) as TilesNeeded;
      continue;
    }
    return readJSON<Forecast>(res);
  }
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

/** The radar frame forecasts start from, as a Leaflet tile URL template, and when the next is expected. */
export type RadarFrame = { time: string; tile_url: string; max_zoom: number; next_due?: string };

export function fetchRadar(): Promise<RadarFrame> {
  return getJSON<RadarFrame>("/api/radar");
}
