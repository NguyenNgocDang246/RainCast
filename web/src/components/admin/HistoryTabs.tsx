"use client";

import type { Frame, Issue, Lookup, Overview, Station } from "@/lib/admin";
import { dateTime, dbz, minutes, time } from "@/lib/adminFormat";
import { compass } from "@/lib/format";
import { useLocale } from "@/lib/i18n";
import { Loading, Panel, Verdict, td, th, useAdminData } from "./ui";

/** Station list from the overview, for labels. */
function useStations(): Station[] {
  const { data } = useAdminData<Overview>("/api/admin/overview", 300_000);
  return data?.pipeline.stations ?? [];
}

/** Station forecasts with each lead's prediction against what came. */
export function IssuesTab() {
  const { locale, t } = useLocale();
  const h = t.admin.history;
  const st = t.admin.state;
  const { data, error } = useAdminData<Issue[]>("/api/admin/issues?limit=100");
  const stations = useStations();
  if (!data) return <Loading error={error} />;
  const name = (id: string) => stations.find((s) => s.id === id)?.name ?? id;
  const leads = data[0]?.leads.map((l) => l.lead_min) ?? [];

  return (
    <Panel title={h.issuesTitle(data.length)}>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[900px] text-sm">
          <thead>
            <tr className="border-b border-slate-800">
              <th className={th}>{h.frame}</th>
              <th className={th}>{h.station}</th>
              <th className={th}>{h.then}</th>
              <th className={th}>{h.rainIn}</th>
              <th className={th}>{h.heavyIn}</th>
              {leads.map((m) => (
                <th key={m} className={th}>
                  {h.predVsObs(m)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {data.map((is) => (
              <tr key={`${is.station}@${is.issued_at}`} className="border-b border-slate-800/60 align-top">
                <td className={`${td} text-slate-100`}>{dateTime(is.issued_at, locale)}</td>
                <td className={td}>{name(is.station)}</td>
                <td className={td}>{is.result.heavy_now ? st.heavy : is.raining_now ? st.raining : st.dry}</td>
                <td className={td}>{is.raining_now ? st.raining : minutes(is.arrival_min, locale)}</td>
                <td className={td}>
                  {is.result.heavy_now ? st.heavyNow : minutes(is.result.heavy_arrival_min ?? -1, locale)}
                </td>
                {is.leads.map((l) => (
                  <td key={l.lead_min} className={td}>
                    <div>
                      {dbz(l.pred_dbz)} / {l.obs_dbz == null ? "…" : dbz(l.obs_dbz)}
                    </div>
                    {l.obs_rain != null && (
                      <div className="text-xs">
                        <Verdict ok={l.pred_rain === l.obs_rain} />
                      </div>
                    )}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="mt-3 text-xs text-slate-500">
        {h.issuesNote}
      </p>
    </Panel>
  );
}

export function FramesTab() {
  const { locale, t } = useLocale();
  const h = t.admin.history;
  const { data, error } = useAdminData<Frame[]>("/api/admin/frames?limit=100");
  const stations = useStations();
  if (!data) return <Loading error={error} />;
  return (
    <Panel title={h.framesTitle(data.length)}>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[900px] text-sm">
          <thead>
            <tr className="border-b border-slate-800">
              <th className={th}>{h.frameTime}</th>
              {stations.map((s) => (
                <th key={s.id} className={th}>
                  {s.name}
                </th>
              ))}
              <th className={th}>{h.recordedAt}</th>
            </tr>
          </thead>
          <tbody>
            {data.map((f) => (
              <tr key={f.time} className="border-b border-slate-800/60">
                <td className={`${td} text-slate-100`}>{dateTime(f.time, locale)}</td>
                {stations.map((s) => (
                  <td key={s.id} className={td}>
                    {dbz(f.obs[s.id])}
                  </td>
                ))}
                <td className={td}>{dateTime(f.recorded_at, locale)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="mt-3 text-xs text-slate-500">{h.framesNote}</p>
    </Panel>
  );
}

/** Every on-demand forecast served to users (one row per place and frame). */
export function LookupsTab({ onInspect }: { onInspect: (lat: number, lon: number) => void }) {
  const { locale, t } = useLocale();
  const h = t.admin.history;
  const st = t.admin.state;
  const { data, error } = useAdminData<Lookup[]>("/api/admin/lookups?limit=200");
  if (!data) return <Loading error={error} />;
  return (
    <Panel title={h.lookupsTitle(data.length)}>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[820px] text-sm">
          <thead>
            <tr className="border-b border-slate-800">
              <th className={th}>{h.at}</th>
              <th className={th}>{h.place}</th>
              <th className={th}>{h.frame}</th>
              <th className={th}>{h.result}</th>
              <th className={th}>{h.rainIn}</th>
              <th className={th}>{h.heavyIn}</th>
              <th className={th}>{h.motion}</th>
              <th className={th} />
            </tr>
          </thead>
          <tbody>
            {data.map((l) => (
              <tr key={l.id} className="border-b border-slate-800/60">
                <td className={`${td} text-slate-100`}>{dateTime(l.at, locale)}</td>
                <td className={`${td} font-mono text-xs`}>
                  {l.lat.toFixed(4)}, {l.lon.toFixed(4)}
                </td>
                <td className={td}>{time(l.frame_time, locale)}</td>
                <td className={td}>{l.heavy_now ? st.heavy : l.raining_now ? st.raining : st.dry}</td>
                <td className={td}>{l.raining_now ? "–" : minutes(l.arrival_min, locale)}</td>
                <td className={td}>{l.heavy_now ? "–" : minutes(l.heavy_arrival_min, locale)}</td>
                <td className={td}>
                  {l.speed_kmh >= 1 ? `${l.speed_kmh.toFixed(0)} km/h ${compass(l.direction_deg, locale)}` : "–"}
                </td>
                <td className={td}>
                  <button
                    type="button"
                    onClick={() => onInspect(l.lat, l.lon)}
                    className="cursor-pointer text-xs text-sky-400 hover:underline"
                  >
                    {h.viewRadar}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {data.length === 0 && <p className="mt-3 text-sm text-slate-500">{h.noLookups}</p>}
    </Panel>
  );
}
