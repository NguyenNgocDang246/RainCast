"use client";

import { useState } from "react";
import type { BenchmarkRow, BenchmarkStatus } from "@/lib/admin";
import { dateTime, pct } from "@/lib/adminFormat";
import { useLocale } from "@/lib/i18n";
import { ErrorText, Panel, td, th, useAdminData } from "./ui";

const DAYS = [7, 30, 90] as const;

/** Lead weatherindex.ai ranks by. */
const RANK_LEAD = 60;

/** A provider not called for this long has likely stopped. */
const STALE_S = 30 * 60;

/** Line colour per forecaster; persistence is grey. */
const COLORS: Record<string, string> = {
  raincast: "#38bdf8",
  tomorrow: "#fbbf24",
  rainbow: "#34d399",
};
const PERSISTENCE_COLOR = "#64748b";

const colorOf = (r: BenchmarkRow) => (r.persistence ? PERSISTENCE_COLOR : (COLORS[r.forecaster] ?? "#a78bfa"));
const keyOf = (r: BenchmarkRow) => `${r.forecaster}-${r.persistence ?? false}`;
const f1At = (r: BenchmarkRow, lead: number) => r.leads.find((c) => c.lead_min === lead)?.scores.f1 ?? null;

/**
 * Scores what `go run ./cmd/benchmark` recorded, against airport METAR as
 * weatherindex.ai does: RainCast, Tomorrow.io and Rainbow.ai by F-score,
 * lead by lead, with persistence as the baseline.
 */
export function BenchmarkPanel() {
  const { locale, t } = useLocale();
  const b = t.admin.benchmark;
  const [days, setDays] = useState<(typeof DAYS)[number]>(7);
  const { data: status, error } = useAdminData<BenchmarkStatus>(`/api/admin/benchmark?days=${days}`, 60_000);
  const report = status?.report ?? null;

  const at = (unix: number) => dateTime(new Date(unix * 1000).toISOString(), locale);
  const num = (v: number | null) => (v == null ? "–" : v.toFixed(3));
  const label = (r: BenchmarkRow) => (r.persistence ? b.persistence : (b.names[r.forecaster] ?? r.forecaster));
  const hourly = report?.providers.some((p) => p.name === "tomorrow" && (p.kinds.hourly ?? 0) > 0);
  const metar = report?.tables.find((tb) => tb.truth === "metar" && tb.rows.some((r) => r.overall.n > 0));
  // Forecasters best first by F at the ranking lead, persistence last.
  const rows = metar
    ? [
        ...metar.rows.filter((r) => !r.persistence).sort((x, y) => (f1At(y, RANK_LEAD) ?? -1) - (f1At(x, RANK_LEAD) ?? -1)),
        ...metar.rows.filter((r) => r.persistence),
      ]
    : [];
  const samples = (lead: number) => metar?.rows[0]?.leads.find((c) => c.lead_min === lead)?.scores.n ?? 0;
  // When the server scored, so stale stays put between renders.
  const now = report ? Date.parse(report.generated_at) / 1000 : 0;

  const daysPicker = (
    <div className="flex rounded-lg border border-slate-800 text-xs">
      {DAYS.map((d) => (
        <button
          key={d}
          type="button"
          onClick={() => setDays(d)}
          className={`px-2.5 py-1 ${d === days ? "bg-slate-800 text-slate-100" : "text-slate-500 hover:text-slate-300"}`}
        >
          {b.days(d)}
        </button>
      ))}
    </div>
  );

  return (
    <Panel title={b.title} action={daysPicker}>
      {error && <ErrorText error={error} className="mb-3" />}
      {status && !report ? (
        <p className="text-sm text-slate-500">{b.noDatabase}</p>
      ) : !report || report.providers.length === 0 ? (
        <p className="text-sm text-slate-500">
          {b.never} <code className="text-xs">{b.howToRun}</code>
        </p>
      ) : (
        <>
          <ul className="mb-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-slate-500">
            {report.providers.map((p) => {
              const stale = now - p.last_issued > STALE_S;
              return (
                <li key={p.name}>
                  <span className="text-slate-300">{b.names[p.name] ?? p.name}</span> · {b.last(at(p.last_issued))}
                  {stale && <span className="ml-1 text-amber-300">⚠ {b.stale}</span>}
                </li>
              );
            })}
          </ul>
          {hourly && (
            <p className="mb-4 rounded-lg border border-amber-400/30 bg-amber-400/10 px-3 py-2 text-sm text-amber-200">
              <span aria-hidden>⚠ </span>
              {b.hourlyWarning}
            </p>
          )}

          {!metar ? (
            <p className="text-sm text-slate-500">{b.noMetar}</p>
          ) : (
            <>
              <p className="mb-4 text-xs text-slate-500">
                {b.summary(at(report.from), at(report.to), report.stations.join(", "), samples(RANK_LEAD))}
              </p>

              <h3 className="mb-2 text-xs font-semibold text-slate-400">{b.rankTitle}</h3>
              <ol className="mb-6 grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
                {rows.map((r, i) => (
                  <li
                    key={keyOf(r)}
                    className={`rounded-lg border px-3 py-2 ${
                      r.persistence ? "border-dashed border-slate-800" : "border-slate-800"
                    }`}
                  >
                    <div className="flex items-center gap-2 text-xs text-slate-400">
                      <span className="inline-block h-2 w-2 rounded-full" style={{ background: colorOf(r) }} />
                      {!r.persistence && <span className="text-slate-500">#{i + 1}</span>}
                      {label(r)}
                    </div>
                    <div className={`mt-1 text-2xl font-semibold ${r.persistence ? "text-slate-500" : "text-slate-50"}`}>
                      {num(f1At(r, RANK_LEAD))}
                    </div>
                  </li>
                ))}
              </ol>

              <FScoreChart rows={rows} leads={report.leads} label={label} />

              <div className="mt-6 overflow-x-auto">
                <table className="w-full min-w-120 text-sm">
                  <thead>
                    <tr className="border-b border-slate-800">
                      <th className={th}>{b.forecaster}</th>
                      {report.leads.map((l) => (
                        <th key={l} className={`${th} ${l === RANK_LEAD ? "text-slate-200" : ""}`}>
                          +{l}′<div className="font-normal text-slate-600">n={samples(l)}</div>
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((r) => (
                      <tr key={keyOf(r)} className="border-b border-slate-800/60">
                        <td className={`${td} whitespace-nowrap ${r.persistence ? "text-slate-500" : "text-slate-100"}`}>
                          {label(r)}
                        </td>
                        {r.leads.map((c) => (
                          <td
                            key={c.lead_min}
                            className={`${td} ${r.persistence ? "text-slate-500" : ""}`}
                            title={b.cellTitle(pct(c.scores.pod), pct(c.scores.far), c.scores.n)}
                          >
                            {num(c.scores.f1)}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}
          <p className="mt-6 text-xs leading-relaxed text-slate-500">{b.note}</p>
        </>
      )}
    </Panel>
  );
}

/** F-score by lead, one line per forecaster, as weatherindex.ai draws it. */
function FScoreChart({
  rows,
  leads,
  label,
}: {
  rows: BenchmarkRow[];
  leads: number[];
  label: (r: BenchmarkRow) => string;
}) {
  const { t } = useLocale();
  const b = t.admin.benchmark;
  const W = 640;
  const H = 240;
  const pad = { l: 44, r: 12, t: 12, b: 32 };
  const values = rows.flatMap((r) => r.leads.map((c) => c.scores.f1).filter((v): v is number => v != null));
  if (values.length === 0) return null;
  const lo = Math.max(0, Math.floor(Math.min(...values) * 20) / 20);
  const hi = Math.min(1, Math.ceil(Math.max(...values) * 20) / 20 || 0.05);
  const span = hi - lo || 0.05;
  const maxLead = leads[leads.length - 1];
  const x = (lead: number) => pad.l + (lead / maxLead) * (W - pad.l - pad.r);
  const y = (v: number) => pad.t + (1 - (v - lo) / span) * (H - pad.t - pad.b);
  const yTicks = Array.from({ length: Math.round(span / 0.05) + 1 }, (_, i) => lo + i * 0.05);

  return (
    <figure>
      <figcaption className="mb-2 text-xs font-semibold text-slate-400">{b.chartTitle}</figcaption>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full max-w-3xl" role="img" aria-label={b.chartTitle}>
        {yTicks.map((v) => (
          <g key={v}>
            <line x1={pad.l} x2={W - pad.r} y1={y(v)} y2={y(v)} className="stroke-slate-800" />
            <text x={pad.l - 6} y={y(v) + 4} textAnchor="end" className="fill-slate-500 text-[10px]">
              {v.toFixed(2)}
            </text>
          </g>
        ))}
        {leads.map((l) => (
          <text key={l} x={x(l)} y={H - pad.b + 14} textAnchor="middle" className="fill-slate-500 text-[10px]">
            {l}
          </text>
        ))}
        <text x={(pad.l + W - pad.r) / 2} y={H - 4} textAnchor="middle" className="fill-slate-500 text-[10px]">
          {b.chartX}
        </text>
        {rows.map((r) => {
          const pts = r.leads.filter((c) => c.scores.f1 != null);
          const color = colorOf(r);
          return (
            <g key={keyOf(r)}>
              <polyline
                fill="none"
                stroke={color}
                strokeWidth={2}
                strokeDasharray={r.persistence ? "4 4" : undefined}
                points={pts.map((c) => `${x(c.lead_min)},${y(c.scores.f1!)}`).join(" ")}
              />
              {pts.map((c) => (
                <circle key={c.lead_min} cx={x(c.lead_min)} cy={y(c.scores.f1!)} r={2.5} fill={color}>
                  <title>{`${label(r)} +${c.lead_min}′: ${c.scores.f1!.toFixed(3)}`}</title>
                </circle>
              ))}
            </g>
          );
        })}
      </svg>
    </figure>
  );
}
