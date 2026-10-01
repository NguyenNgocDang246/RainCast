"use client";

import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { fetchForecast, type Forecast, type Place } from "@/lib/api";
import { ForecastCard } from "./ForecastCard";
import { Intro } from "./Intro";
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

export function Dashboard() {
  const raw = useSyncExternalStore(subscribe, readRaw, () => undefined);
  /** null = nothing chosen yet; undefined = not hydrated yet. */
  const place = useMemo(() => parsePlace(raw), [raw]);
  const [forecast, setForecast] = useState<Forecast | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (!place) return; // no place chosen yet: the intro is shown instead
    let cancelled = false;
    const load = async () => {
      try {
        const f = await fetchForecast(place);
        if (cancelled) return;
        setForecast(f);
        setError(null);
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      }
      if (!cancelled) setNow(Date.now());
    };
    load();
    const id = setInterval(load, REFRESH_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [place]);

  const select = (p: Place | null) => {
    // Re-selecting the current place keeps the shown forecast; the effect
    // only reruns when the stored value actually changes.
    if ((p ? JSON.stringify(p) : null) !== readRaw()) {
      setForecast(null);
      setError(null);
    }
    savePlace(p);
  };

  return (
    <main className="flex flex-1 flex-col items-center px-4 py-10">
      <LocationSearch onSelect={select} near={place ?? null} />

      {place && (
        <p className="mt-4 text-sm text-slate-400">
          <span className="text-slate-200">{place.name}</span>
          <button
            type="button"
            onClick={() => select(null)}
            className="ml-3 text-sky-400 hover:underline cursor-pointer"
          >
            Bỏ chọn
          </button>
        </p>
      )}

      <div className="flex w-full flex-1 flex-col items-center justify-center py-12">
        {place === null && <Intro />}
        {place && forecast && <ForecastCard forecast={forecast} now={now} />}
        {place && !forecast && !error && (
          <p className="animate-pulse text-slate-500" aria-busy="true">
            Đang xem radar…
          </p>
        )}
        {place && error && (
          <p className="mt-8 max-w-md text-center text-sm text-amber-300">{error}</p>
        )}
      </div>

      <footer className="text-center text-xs text-slate-600">
        Radar: RainViewer · Địa chỉ: ©{" "}
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
