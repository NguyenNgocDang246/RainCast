"use client";

import { useEffect, useState } from "react";
import type { Forecast, Place } from "@/lib/api";
import { geocode } from "@/lib/api";
import { adminGet, adminImage } from "@/lib/admin";
import { dbz, minutes, time } from "@/lib/adminFormat";
import { compass } from "@/lib/format";
import { useLocale } from "@/lib/i18n";
import { ErrorText, Panel, td, th } from "./ui";

type Target = { lat: number; lon: number } | null; // null = home location

/** Inspect any location: radar mosaic with motion, and the full forecast. */
export function ToolsTab({ initial }: { initial: Target }) {
  const { t } = useLocale();
  const tt = t.admin.tools;
  const [target, setTarget] = useState<Target>(initial);
  const [input, setInput] = useState(initial ? `${initial.lat}, ${initial.lon}` : "");
  const [places, setPlaces] = useState<Place[] | null>(null);
  const [img, setImg] = useState<string | null>(null);
  const [forecast, setForecast] = useState<Forecast | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    // Forecasts need a location; there is no default one. Without a
    // target the panels below are not shown.
    if (!target) return;
    let cancelled = false;
    let url: string | null = null;
    const q = `?lat=${target.lat}&lon=${target.lon}`;
    (async () => {
      try {
        const [f, u] = await Promise.all([
          adminGet<Forecast>(`/api/admin/forecast${q}`),
          adminImage(`/api/admin/radar.png${q}`),
        ]);
        url = u;
        if (cancelled) return URL.revokeObjectURL(u);
        setForecast(f);
        setImg(u);
        setError(null);
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
      if (url) URL.revokeObjectURL(url);
    };
  }, [target]);

  // Address, "lat, lon" or a Google Maps link, through the same resolver
  // the public search uses.
  const search = async (e: React.SubmitEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!input.trim()) return setTarget(null);
    setBusy(true);
    setPlaces(null);
    try {
      const found = await geocode(input.trim());
      if (found.length === 1) setTarget({ lat: found[0].lat, lon: found[0].lon });
      setPlaces(found);
      if (!found.length) setError(tt.notFound);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <Panel title={tt.inspect}>
        <form onSubmit={search} className="flex gap-2">
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder={tt.placeholder}
            className="min-w-0 flex-1 rounded-xl border border-slate-800 bg-slate-900 px-4 py-2.5 text-sm text-slate-100 placeholder:text-slate-500 focus:border-sky-500 focus:outline-none"
          />
          <button
            type="submit"
            disabled={busy}
            className="cursor-pointer rounded-xl bg-sky-500 px-4 text-sm font-medium text-slate-950 hover:bg-sky-400 disabled:opacity-50"
          >
            {busy ? tt.busy : tt.view}
          </button>
        </form>
        {places && places.length > 1 && (
          <ul className="mt-3 divide-y divide-slate-800 rounded-xl border border-slate-800 text-sm">
            {places.map((p) => (
              <li key={`${p.lat},${p.lon},${p.name}`}>
                <button
                  type="button"
                  onClick={() => setTarget({ lat: p.lat, lon: p.lon })}
                  className="block w-full cursor-pointer px-4 py-2 text-left hover:bg-slate-800"
                >
                  <span className="text-slate-100">{p.name}</span>
                  <span className="ml-2 font-mono text-xs text-slate-500">
                    {p.lat.toFixed(4)}, {p.lon.toFixed(4)}
                  </span>
                  {p.address && <span className="block truncate text-xs text-slate-500">{p.address}</span>}
                </button>
              </li>
            ))}
          </ul>
        )}
        {error && <ErrorText error={error} className="mt-3" />}
      </Panel>

      {!target ? (
        <p className="text-sm text-slate-500">{tt.pickPlace}</p>
      ) : (
      <div className="grid gap-6 lg:grid-cols-2">
        <Panel title={tt.radarTitle}>
          {img ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={img} alt={tt.radarAlt} className="aspect-square w-full rounded-xl border border-slate-800" />
          ) : (
            <div className="aspect-square animate-pulse rounded-xl bg-slate-900" />
          )}
          <p className="mt-2 text-xs text-slate-500">
            {tt.legend}
          </p>
        </Panel>

        <Panel title={tt.detail}>
          {forecast ? <ForecastDetail f={forecast} /> : <p className="animate-pulse text-sm text-slate-500">{t.admin.ui.loading}</p>}
        </Panel>
      </div>
      )}
    </div>
  );
}

function ForecastDetail({ f }: { f: Forecast }) {
  const { locale, t } = useLocale();
  const tt = t.admin.tools;
  const st = t.admin.state;
  return (
    <div className="space-y-4 text-sm">
      <dl className="grid grid-cols-2 gap-x-4 gap-y-2">
        <Row k={tt.place} v={`${f.location.lat}, ${f.location.lon}`} />
        <Row k={tt.frame} v={time(f.frame_time, locale)} />
        <Row k={tt.now} v={f.heavy_now ? st.heavy : f.raining_now ? st.raining : st.dry} />
        <Row k={tt.rainIn} v={f.raining_now ? st.raining : minutes(f.arrival_min, locale)} />
        <Row k={tt.heavyIn} v={f.heavy_now ? st.heavyNow : minutes(f.heavy_arrival_min, locale)} />
        <Row
          k={tt.motion}
          v={
            f.motion_reliable
              ? tt.motionValue(f.speed_kmh.toFixed(1), compass(f.direction_deg, locale), f.direction_deg.toFixed(0))
              : tt.noMotion
          }
        />
        {f.motion_coherence !== undefined && <Row k={tt.coherence} v={f.motion_coherence.toFixed(2)} />}
        <Row k={tt.gain} v={f.motion_gain !== undefined ? f.motion_gain.toFixed(2) : "—"} />
        {f.motion_members?.map((m) => (
          <Row
            key={m.method}
            k={tt.member(m.method)}
            v={tt.motionValue(m.speed_kmh.toFixed(1), compass(m.direction_deg, locale), m.direction_deg.toFixed(0))}
          />
        ))}
        <Row k={tt.threshold} v={tt.thresholdValue(f.threshold_dbz, f.heavy_dbz)} />
      </dl>
      <div className="max-h-72 overflow-y-auto">
        <table className="w-full">
          <thead className="sticky top-0 bg-slate-900">
            <tr className="border-b border-slate-800">
              <th className={th}>{tt.minuteFromFrame}</th>
              <th className={th}>{tt.predictedDbz}</th>
            </tr>
          </thead>
          <tbody>
            {f.series
              .filter((p) => p.minute % 5 === 0)
              .map((p) => (
                <tr key={p.minute} className="border-b border-slate-800/60">
                  <td className={td}>+{p.minute}</td>
                  <td className={td}>{dbz(p.dbz)}</td>
                </tr>
              ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <>
      <dt className="text-slate-500">{k}</dt>
      <dd className="text-slate-200">{v}</dd>
    </>
  );
}
