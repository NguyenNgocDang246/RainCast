"use client";

import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { fetchForecast, reverse, type Forecast, type Place } from "@/lib/api";
import { pollFrames, type FrameInfo } from "@/lib/framePoll";
import { useLocale, userError } from "@/lib/i18n";
import { addRecent } from "@/lib/recent";
import { ForecastCard } from "./ForecastCard";
import { Intro } from "./Intro";
import { LanguageSwitch } from "./LanguageSwitch";
import { LocationMap } from "./LocationMap";
import { LocationSearch } from "./LocationSearch";

const STORAGE_KEY = "raincast.place";
const LOCATE_DEADLINE_MS = 30_000;
/** A problem shown in the sheet: raised above the forecast's backdrop, which spills past the card. */
const NOTICE = "relative z-10 text-sm text-amber-300";

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
    // A forecast only changes with a new radar frame, so it is fetched when one is due.
    const stop = pollFrames(async () => {
      let frame: FrameInfo | null = null;
      try {
        const f = await fetchForecast({ lat, lon });
        if (cancelled) return null;
        setResult({ key: k, forecast: f, error: null });
        frame = { time: f.frame_time, next_due: f.next_due };
      } catch (e) {
        if (cancelled) return null;
        const msg = e instanceof Error ? e.message : String(e);
        // Visitors see a plain line (userError); the details stay here.
        console.warn("forecast:", msg);
        // A failed refresh keeps the last good forecast for this point.
        setResult((r) => ({ key: k, forecast: r?.key === k ? r.forecast : null, error: msg }));
      }
      setNow(Date.now());
      return frame;
    });
    return () => {
      cancelled = true;
      stop();
    };
  }, [lat, lon]);

  // The forecast is read as of now (ForecastCard), so the clock moves on
  // each minute, on the minute, between fetches.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>;
    const tick = () => {
      setNow(Date.now());
      timer = setTimeout(tick, 60_000 - (Date.now() % 60_000));
    };
    timer = setTimeout(tick, 60_000 - (Date.now() % 60_000));
    return () => clearTimeout(timer);
  }, []);

  // Only a result fetched for the current point is shown, so moving the point
  // (from any source, including another tab) shows loading instead of the old
  // forecast; renaming the same point keeps it.
  const current = result?.key === key ? result : null;
  const forecast = current?.forecast ?? null;
  const error = current?.error ?? null;

  const [locating, setLocating] = useState(false);
  /** Which locate problem to show, translated as it renders so it follows the language. */
  const [locateError, setLocateError] = useState<"failed" | "insecure" | "denied" | null>(null);
  /** The running locate attempt: its position watch and its deadline. */
  const locateRun = useRef<{ watch: number; deadline: ReturnType<typeof setTimeout> } | null>(null);

  const stopLocating = () => {
    const run = locateRun.current;
    if (!run) return;
    navigator.geolocation.clearWatch(run.watch);
    clearTimeout(run.deadline);
    locateRun.current = null;
    setLocating(false);
  };

  useEffect(
    () => () => {
      const run = locateRun.current;
      if (!run) return;
      navigator.geolocation.clearWatch(run.watch);
      clearTimeout(run.deadline);
    },
    [],
  );

  /** Choosing any place clears a failed locate attempt and drops a running one. */
  const select = (p: Place | null) => {
    stopLocating();
    setLocateError(null);
    savePlace(p);
  };

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

  /** Uses the browser's position like a point picked on the map. */
  const locate = () => {
    if (!("geolocation" in navigator)) {
      setLocateError("failed");
      return;
    }
    // Browsers only share the position with https pages (and localhost).
    if (!window.isSecureContext) {
      setLocateError("insecure");
      return;
    }
    stopLocating();
    setLocating(true);
    setLocateError(null);
    // A coarse (network) position is enough for a radar cell and comes at
    // once; asking for GPS made a cold device time out. The watch rides out
    // transient errors until the first fix or our own deadline.
    const watch = navigator.geolocation.watchPosition(
      (pos) => {
        stopLocating();
        pickOnMap(pos.coords.latitude, pos.coords.longitude);
      },
      (err) => {
        if (err.code !== err.PERMISSION_DENIED) return;
        stopLocating();
        setLocateError("denied");
      },
      { enableHighAccuracy: false, maximumAge: 10 * 60_000 },
    );
    const deadline = setTimeout(() => {
      stopLocating();
      setLocateError("failed");
    }, LOCATE_DEADLINE_MS);
    locateRun.current = { watch, deadline };
  };

  // Google Maps layout: the map fills the screen above a thin footer; search
  // and results float over it (left column on wide screens, bottom sheet on phones).
  return (
    <main className="flex h-dvh flex-col overflow-hidden">
      <div className="relative flex-1">
        <LocationMap place={place ?? null} onPick={pickOnMap} />
        <LanguageSwitch title="title" />

        <div className="pointer-events-none absolute inset-x-0 top-0 bottom-0 z-10 flex flex-col justify-between gap-3 p-3 sm:inset-x-auto sm:left-0 sm:w-md sm:justify-start lg:w-132 sm:p-4">
          <div className="pointer-events-auto mr-20 sm:mr-0">
            <LocationSearch
              onSelect={select}
              value={place ?? null}
              near={place ?? null}
              onLocate={locate}
              locating={locating}
            />
          </div>

          <section className="pointer-events-auto max-h-[45dvh] overflow-y-auto rounded-2xl border border-slate-800 bg-slate-950/95 p-5 shadow-2xl shadow-black/40 backdrop-blur sm:max-h-none sm:min-h-0">
            {locateError && <p className={`${NOTICE} mb-6`}>{t.locate[locateError]}</p>}
            {place === null && <Intro onLocate={locate} locating={locating} />}
            {place && forecast && <ForecastCard forecast={forecast} now={now} />}
            {place && !forecast && !error && (
              <p className="animate-pulse text-slate-500" aria-busy="true">
                {t.dashboard.loading}
              </p>
            )}
            {/* A failed refresh under the last good forecast stays clear of its backdrop. */}
            {place && error && <p className={`${NOTICE} ${forecast ? "mt-6" : ""}`}>{userError(t, error)}</p>}
          </section>
        </div>
      </div>

      <footer className="shrink-0 border-t border-slate-800 px-4 py-1.5 text-center text-xs text-slate-600">
        Radar: RainViewer · {t.dashboard.footerAddress}:{" "}
        <a
          href="https://www.geoapify.com/"
          className="hover:text-slate-400"
          target="_blank"
          rel="noreferrer"
        >
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
        contributors ·{" "}
        <a href="https://db-ip.com" className="hover:text-slate-400" target="_blank" rel="noreferrer">
          IP Geolocation by DB-IP
        </a>
      </footer>
    </main>
  );
}
