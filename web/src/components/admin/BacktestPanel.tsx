"use client";

import type { BacktestResult, BacktestStatus } from "@/lib/admin";
import { MIN_EVENTS } from "@/lib/admin";
import { dateTime, pct } from "@/lib/adminFormat";
import { useLocale } from "@/lib/i18n";
import { ErrorText, Panel, td, th, useAdminData } from "./ui";

/**
 * Shows the report `go run ./cmd/backtest` last wrote: every stored radar
 * frame, over the station region and every collected region, scored with
 * each forecast method. Backtests never run from this page.
 */
export function BacktestPanel() {
  const { locale, t } = useLocale();
  const b = t.admin.backtest;
  const { data: status, error } = useAdminData<BacktestStatus>("/api/admin/backtest", 60_000);
  const report = status?.report ?? null;

  const results = report?.results ?? [];
  const leads = results[0]?.leads.map((l) => l.lead_min) ?? [];
  // Reports saved before method/pairs/trend/baseline existed only have the
  // Vietnamese name ("4 cặp + xu hướng"); the baseline is always row 0.
  const label = (r: BacktestResult, i: number) => {
    if (r.baseline ?? i === 0) return b.baseline;
    if (r.method && b.methods[r.method]) {
      const pairs = r.method === "trec" && r.pairs ? b.pairsSuffix(r.pairs) : "";
      return b.methods[r.method] + pairs + (r.trend ? b.trendSuffix : "") + (r.accel ? b.accelSuffix : "");
    }
    const m = r.name.match(/^(\d+) cặp( \+ xu hướng)?$/);
    const pairs = r.pairs ?? (m ? Number(m[1]) : 0);
    return pairs ? b.variant(pairs, r.trend ?? !!m?.[2]) : r.name;
  };
  const ci = (v?: [number, number]) =>
    v ? ` [${Math.round(v[0] * 100)}–${Math.round(v[1] * 100)}]` : "";
  const num = (v?: number | null) => (v == null ? "–" : v.toFixed(2));
  // The best overall CSI among model variants (the baseline is row 0).
  const best = results
    .slice(1)
    .reduce<number | null>((m, r) => Math.max(m ?? 0, r.overall.csi ?? 0), null);

  return (
    <Panel
      title={b.title}
      action={<code className="text-xs text-slate-500">{b.howToRun}</code>}
    >
      {error && <ErrorText error={error} className="mb-3" />}
      {!report ? (
        <p className="text-sm text-slate-500">{b.never}</p>
      ) : report.issues === 0 ? (
        <p className="text-sm text-slate-500">{b.notEnough(report.frames)}</p>
      ) : (
        <>
          <p className="mb-4 text-xs text-slate-500">
            {b.summary(
              report.issues,
              dateTime(new Date(report.from * 1000).toISOString(), locale),
              dateTime(new Date(report.to * 1000).toISOString(), locale),
              report.points,
              report.frames,
              dateTime(report.generated_at, locale),
              report.duration,
            )}
            {report.events != null && <> {b.events(report.events, report.regions?.length ?? 1)}</>}
          </p>
          {report.events != null && report.events < MIN_EVENTS && (
            <p className="mb-4 rounded-lg border border-amber-400/30 bg-amber-400/10 px-3 py-2 text-sm text-amber-200">
              <span aria-hidden>⚠ </span>
              {b.fewEvents(MIN_EVENTS)}
            </p>
          )}
          <div className="overflow-x-auto">
            <table className="w-full min-w-270 text-sm">
              <thead>
                <tr className="border-b border-slate-800">
                  <th className={th}>{b.config}</th>
                  {leads.map((l) => (
                    <th key={l} className={th}>
                      CSI +{l}′
                    </th>
                  ))}
                  <th className={th}>{b.csiCi}</th>
                  <th className={th}>{b.delta}</th>
                  <th className={th}>{b.bss}</th>
                  <th className={th}>{b.auc}</th>
                  <th className={th}>{b.accum}</th>
                  <th className={th}>{b.ms}</th>
                </tr>
              </thead>
              <tbody>
                {results.map((r, i) => {
                  const top = i > 0 && best != null && (r.overall.csi ?? 0) >= best;
                  return (
                    <tr key={r.name} className="border-b border-slate-800/60">
                      <td
                        className={`${td} ${top ? "font-semibold text-slate-50" : "text-slate-100"}`}
                      >
                        {label(r, i)}
                        {top && (
                          <span className="ml-2 text-xs font-normal text-sky-400">{b.best}</span>
                        )}
                      </td>
                      {r.leads.map((l) => (
                        <td key={l.lead_min} className={td}>
                          {pct(l.scores.csi)}
                        </td>
                      ))}
                      <td
                        className={`${td} whitespace-nowrap ${top ? "font-semibold text-slate-50" : ""}`}
                      >
                        {pct(r.overall.csi)}
                        <span className="text-xs text-slate-500">{ci(r.csi_ci)}</span>
                      </td>
                      <td className={`${td} whitespace-nowrap`}>
                        <Delta r={r} better={b.better} worse={b.worse} />
                      </td>
                      <td className={td}>{num(r.bss)}</td>
                      <td className={td}>{num(r.auc)}</td>
                      <td className={td}>{num(r.accum_mae_mm)}</td>
                      <td className={td}>
                        {r.ms_per_issue == null ? "–" : Math.round(r.ms_per_issue)}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          {report.groups && report.groups.length > 1 && (
            <>
              <h3 className="mt-6 mb-2 text-xs font-semibold text-slate-400">{b.groups}</h3>
              <div className="overflow-x-auto">
                <table className="w-full min-w-140 text-sm">
                  <thead>
                    <tr className="border-b border-slate-800">
                      <th className={th}>{b.group}</th>
                      <th className={th}>{b.regionsCol}</th>
                      <th className={th}>{b.eventsCol}</th>
                      <th className={th}>{b.baselineCsi}</th>
                      <th className={th}>{b.bestCol}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.groups.map((g) => {
                      const top = g.results
                        .slice(1)
                        .reduce<BacktestResult | null>(
                          (m, r) => ((r.overall.csi ?? -1) > (m?.overall.csi ?? -1) ? r : m),
                          null,
                        );
                      return (
                        <tr key={g.climate} className="border-b border-slate-800/60">
                          <td className={`${td} text-slate-100`}>
                            {b.climates[g.climate] ?? g.climate}
                          </td>
                          <td className={td}>{g.regions}</td>
                          <td className={td}>{g.events}</td>
                          <td className={td}>{pct(g.results[0]?.overall.csi)}</td>
                          <td className={td}>
                            {top ? `${label(top, 1)} · ${pct(top.overall.csi)}` : "–"}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </>
          )}
          <p className="mt-3 text-xs leading-relaxed text-slate-500">
            {report.blocks != null && <>{b.intervals(report.blocks)} </>}
            {b.note}
          </p>
        </>
      )}
    </Panel>
  );
}

const signed = (v: number) => `${v > 0 ? "+" : ""}${(v * 100).toFixed(1)}`;

/** Difference from the reference with its interval; ▲/▼ only when the whole interval clears 0. */
function Delta({ r, better, worse }: { r: BacktestResult; better: string; worse: string }) {
  if (r.delta_csi == null) return <>–</>;
  const iv = r.delta_csi_ci;
  const sure = iv ? (iv[0] > 0 ? "up" : iv[1] < 0 ? "down" : null) : null;
  return (
    <>
      {sure === "up" && (
        <span className="text-[#0ca30c]" title={better} aria-label={better}>
          ▲{" "}
        </span>
      )}
      {sure === "down" && (
        <span className="text-[#d03b3b]" title={worse} aria-label={worse}>
          ▼{" "}
        </span>
      )}
      {signed(r.delta_csi)}
      {iv && (
        <span className="text-xs text-slate-500">
          {" "}
          [{signed(iv[0])}, {signed(iv[1])}]
        </span>
      )}
    </>
  );
}
