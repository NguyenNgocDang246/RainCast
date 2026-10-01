"use client";

import { useEffect, useState } from "react";
import type { Forecast, Place } from "@/lib/api";
import { geocode } from "@/lib/api";
import { adminGet, adminImage } from "@/lib/admin";
import { dbz, minutes, time } from "@/lib/adminFormat";
import { compass } from "@/lib/format";
import { Panel, td, th } from "./ui";

type Target = { lat: number; lon: number } | null; // null = home location

/** Inspect any location: radar mosaic with motion, and the full forecast. */
export function ToolsTab({ initial }: { initial: Target }) {
  const [target, setTarget] = useState<Target>(initial);
  const [input, setInput] = useState(initial ? `${initial.lat}, ${initial.lon}` : "");
  const [places, setPlaces] = useState<Place[] | null>(null);
  const [img, setImg] = useState<string | null>(null);
  const [forecast, setForecast] = useState<Forecast | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let url: string | null = null;
    const q = target ? `?lat=${target.lat}&lon=${target.lon}` : "";
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
      if (!found.length) setError("Không tìm thấy địa điểm.");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <Panel title="Xem một vị trí">
        <form onSubmit={search} className="flex gap-2">
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="Địa chỉ, link Google Maps, tọa độ — để trống = điểm theo dõi"
            className="min-w-0 flex-1 rounded-xl border border-slate-800 bg-slate-900 px-4 py-2.5 text-sm text-slate-100 placeholder:text-slate-500 focus:border-sky-500 focus:outline-none"
          />
          <button
            type="submit"
            disabled={busy}
            className="cursor-pointer rounded-xl bg-sky-500 px-4 text-sm font-medium text-slate-950 hover:bg-sky-400 disabled:opacity-50"
          >
            {busy ? "Đang tìm…" : "Xem"}
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
        {error && <p className="mt-3 text-sm text-amber-300">Lỗi: {error}</p>}
      </Panel>

      <div className="grid gap-6 lg:grid-cols-2">
        <Panel title="Ảnh radar và chuyển động">
          {img ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={img} alt="Radar quanh vị trí" className="aspect-square w-full rounded-xl border border-slate-800" />
          ) : (
            <div className="aspect-square animate-pulse rounded-xl bg-slate-900" />
          )}
          <p className="mt-2 text-xs text-slate-500">
            Chấm đỏ: vị trí · vòng 25/50/100 km · mũi tên: quãng đường mưa đi trong 60 phút · lưới: ranh giới tile z7
          </p>
        </Panel>

        <Panel title="Dự báo chi tiết">
          {forecast ? <ForecastDetail f={forecast} /> : <p className="animate-pulse text-sm text-slate-500">Đang tải…</p>}
        </Panel>
      </div>
    </div>
  );
}

function ForecastDetail({ f }: { f: Forecast }) {
  return (
    <div className="space-y-4 text-sm">
      <dl className="grid grid-cols-2 gap-x-4 gap-y-2">
        <Row k="Vị trí" v={`${f.location.lat}, ${f.location.lon}`} />
        <Row k="Khung radar" v={time(f.frame_time)} />
        <Row k="Hiện tại" v={f.heavy_now ? "mưa to" : f.raining_now ? "đang mưa" : "khô"} />
        <Row k="Mưa tới sau" v={f.raining_now ? "đang mưa" : minutes(f.arrival_min)} />
        <Row k="Mưa to sau" v={f.heavy_now ? "đang mưa to" : minutes(f.heavy_arrival_min)} />
        <Row
          k="Chuyển động"
          v={f.motion_reliable ? `${f.speed_kmh.toFixed(1)} km/h về ${compass(f.direction_deg)} (${f.direction_deg.toFixed(0)}°)` : "không đủ dữ liệu"}
        />
        <Row k="Ngưỡng" v={`mưa ≥ ${f.threshold_dbz} dBZ · mưa to ≥ ${f.heavy_dbz} dBZ`} />
      </dl>
      <div className="max-h-72 overflow-y-auto">
        <table className="w-full">
          <thead className="sticky top-0 bg-slate-900">
            <tr className="border-b border-slate-800">
              <th className={th}>Phút (từ khung)</th>
              <th className={th}>dBZ dự báo</th>
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
