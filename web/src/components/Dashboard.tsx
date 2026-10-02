"use client";

import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { fetchForecast, reverse, type Forecast, type Place } from "@/lib/api";
import { errorText, useLocale } from "@/lib/i18n";
import { addRecent } from "@/lib/recent";
import { ForecastCard } from "./ForecastCard";
import { Intro } from "./Intro";
import { LanguageSwitch } from "./LanguageSwitch";
import { LocationMap } from "./LocationMap";
import { LocationSearch } from "./LocationSearch";

const REFRESH_MS = 60_000;
const STORAGE_KEY = "raincast.place";

// The chosen place lives in localStorage, read through useSyncExternalStore
// so the prerendered HTML (no storage) and the client agree on first render.
const listeners = new Set<() => void>();

function subscribe(cb: () => void) {
  listeners.add(cb);
  window.addEventListener("storage", cb);
  return () => {
    listeners.delete(cb);
    window.removeEventListener("storage", cb);
  };
}

function readRaw(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

function savePlace(p: Place | null) {
  try {
    if (p) localStorage.setItem(STORAGE_KEY, JSON.stringify(p));
    else localStorage.removeItem(STORAGE_KEY);
  } catch {
    // Storage unavailable (private mode); the choice just won't persist.
  }
  listeners.forEach((cb) => cb());
}

function parsePlace(raw: string | null | undefined): Place | null | undefined {
  if (raw === undefined || raw === null) return raw;
  try {
    return JSON.parse(raw) as Place;
  } catch {
    return null;
  }
}

/** A fetch outcome, tagged with the point ("lat,lon") it belongs to. */
type Result = { key: string; forecast: Forecast | null; error: string | null };

export function Dashboard() {
  const { t } = useLocale();
  const raw = useSyncExternalStore(subscribe, readRaw, () => undefined);
  /** null = nothing chosen yet; undefined = not hydrated yet. */
  const place = useMemo(() => parsePlace(raw), [raw]);
  const [result, setResult] = useState<Result | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const naming = useRef<AbortController | null>(null);

  // Keyed by the point only: naming a picked point must not refetch.
  const lat = place?.lat;
  const lon = place?.lon;
  const key = lat === undefined || lon === undefined ? null : `${lat},${lon}`;
  useEffect(() => {
    // No place chosen yet: the intro is shown instead.
    if (lat === undefined || lon === undefined) return;
    const k = `${lat},${lon}`;
    let cancelled = false;
    const load = async () => {
      try {
        const f = await fetchForecast({ lat, lon });
        if (cancelled) return;
        setResult({ key: k, forecast: f, error: null });
      } catch (e) {
        if (cancelled) return;
        const msg = e instanceof Error ? e.message : String(e);
        // A failed refresh keeps the last good forecast for this point.
        setResult((r) => ({ key: k, forecast: r?.key === k ? r.forecast : null, error: msg }));
      }
      if (!cancelled) setNow(Date.now());
    };
    load();
    const id = setInterval(load, REFRESH_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [lat, lon]);

  // Only a result fetched for the current point is shown, so moving the point
  // (from any source, including another tab) shows loading instead of the old
  // forecast; renaming the same point keeps it.
  const current = result?.key === key ? result : null;
  const forecast = current?.forecast ?? null;
  const error = current?.error ?? null;

  const select = savePlace;

  /** A point from the map is used at once, then named when the lookup returns. */
  const pickOnMap = async (pointLat: number, pointLon: number) => {
    const round = (v: number) => Math.round(v * 1e5) / 1e5;
    const p: Place = {
      name: `${pointLat.toFixed(5)}, ${pointLon.toFixed(5)}`,
      address: "",
      lat: round(pointLat),
      lon: round(pointLon),
    };
    naming.current?.abort();
    const ctrl = new AbortController();
    naming.current = ctrl;
    select(p);
    try {
      const named = await reverse(p.lat, p.lon, ctrl.signal);
      const cur = parsePlace(readRaw());
      // Skip if another place was chosen meanwhile.
      if (ctrl.signal.aborted || !cur || cur.lat !== p.lat || cur.lon !== p.lon) return;
      // Keep the exact point; the server rounds it.
      const full = { ...p, name: named.name || p.name, address: named.address };
      addRecent(full);
      select(full);
    } catch {
      // Naming is best-effort; the coordinates already work.
    }
  };

  return (
    <main className="flex flex-1 flex-col items-center px-4 py-10">
      <LanguageSwitch title="title" />
      <LocationSearch onSelect={select} value={place ?? null} near={place ?? null} />
      <LocationMap place={place ?? null} onPick={pickOnMap} />

      {place && (
        <p className="mt-4 text-sm text-slate-400">
          <span className="text-slate-200">{place.name}</span>
          <button
            type="button"
            onClick={() => select(null)}
            className="ml-3 text-sky-400 hover:underline cursor-pointer"
          >
            {t.dashboard.unselect}
          </button>
        </p>
      )}

      <div className="flex w-full flex-1 flex-col items-center justify-center py-12">
        {place === null && <Intro />}
        {place && forecast && <ForecastCard forecast={forecast} now={now} />}
        {place && !forecast && !error && (
          <p className="animate-pulse text-slate-500" aria-busy="true">
            {t.dashboard.loading}
          </p>
        )}
        {place && error && (
          <p className="mt-8 max-w-md text-center text-sm text-amber-300">{errorText(t, error)}</p>
        )}
      </div>

      <footer className="text-center text-xs text-slate-600">
        Radar: RainViewer · {t.dashboard.footerAddress}:{" "}
        <a href="https://www.geoapify.com/" className="hover:text-slate-400" target="_blank" rel="noreferrer">
          Geoapify
        </a>
        , ©{" "}
        <a
          href="https://www.openstreetmap.org/copyright"
          className="hover:text-slate-400"
          target="_blank"
          rel="noreferrer"
        >
          OpenStreetMap
        </a>{" "}
        contributors
      </footer>
    </main>
  );
}
