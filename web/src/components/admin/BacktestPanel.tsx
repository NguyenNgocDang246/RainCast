"use client";

import { useState } from "react";
import type { BacktestReport, BacktestResult, Scores } from "@/lib/admin";
import { adminPost } from "@/lib/admin";
import { dateTime, pct } from "@/lib/adminFormat";
import { useLocale } from "@/lib/i18n";
import { ErrorText, Panel, td, th, useAdminData } from "./ui";

/**
 * Re-scores every stored radar frame with several model settings, on ~1,000
 * points around the primary station, so settings can be compared on far
 * more cases than the stations alone provide.
 */
export function BacktestPanel() {
  const { locale, t } = useLocale();
  const b = t.admin.backtest;
  const { data: saved, error } = useAdminData<BacktestReport | null>("/api/admin/backtest", 600_000);
  const [fresh, setFresh] = useState<BacktestReport | null>(null);
  const [running, setRunning] = useState(false);
  const [runError, setRunError] = useState<string | null>(null);
  const report = fresh ?? saved;

  const run = async () => {
    setRunning(true);
    setRunError(null);
    try {
      setFresh(await adminPost<BacktestReport>("/api/admin/backtest"));
    } catch (e) {
      setRunError(e instanceof Error ? e.message : String(e));
    } finally {
      setRunning(false);
    }
  };

  const results = report?.results ?? [];
  const leads = results[0]?.leads.map((l) => l.lead_min) ?? [];
  // Reports saved before pairs/trend/baseline existed only have the
  // Vietnamese name ("4 cặp + xu hướng"); the baseline is always row 0.
  const label = (r: BacktestResult, i: number) => {
    if (r.baseline ?? i === 0) return b.baseline;
    const m = r.name.match(/^(\d+) cặp( \+ xu hướng)?$/);
    const pairs = r.pairs ?? (m ? Number(m[1]) : 0);
    return pairs ? b.variant(pairs, r.trend ?? !!m?.[2]) : r.name;
  };
  // The best overall CSI among model variants (the baseline is row 0).
  const best = results.slice(1).reduce<number | null>((m, r) => Math.max(m ?? 0, r.overall.csi ?? 0), null);

  return (
    <Panel
      title={b.title}
      action={
        <button
          type="button"
          onClick={run}
          disabled={running}
          className="cursor-pointer rounded-lg bg-sky-500 px-3 py-1.5 text-xs font-medium text-slate-950 hover:bg-sky-400 disabled:cursor-wait disabled:opacity-60"
        >
          {running ? b.running : b.run}
        </button>
      }
    >
      {(runError || error) && <ErrorText error={(runError ?? error)!} className="mb-3" />}
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
          </p>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead>
                <tr className="border-b border-slate-800">
                  <th className={th}>{b.config}</th>
                  {leads.map((l) => (
                    <th key={l} className={th}>
                      CSI +{l}′
                    </th>
                  ))}
                  <th className={th}>{b.csiTotal}</th>
                  <th className={th}>{t.admin.accuracy.metrics.pod}</th>
                  <th className={th}>{t.admin.accuracy.metrics.far}</th>
                </tr>
              </thead>
              <tbody>
                {results.map((r, i) => {
                  const top = i > 0 && best != null && (r.overall.csi ?? 0) >= best;
                  return (
                    <tr key={r.name} className="border-b border-slate-800/60">
                      <td className={`${td} ${top ? "font-semibold text-slate-50" : "text-slate-100"}`}>
                        {label(r, i)}
                        {top && <span className="ml-2 text-xs font-normal text-sky-400">{b.best}</span>}
                      </td>
                      {r.leads.map((l) => (
                        <td key={l.lead_min} className={td}>
                          {pct(l.scores.csi)}
                        </td>
                      ))}
                      <Strong s={r.overall} k="csi" bold={top} />
                      <Strong s={r.overall} k="pod" />
                      <Strong s={r.overall} k="far" />
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <p className="mt-3 text-xs leading-relaxed text-slate-500">
            {b.note}
          </p>
        </>
      )}
    </Panel>
  );
}

function Strong({ s, k, bold }: { s: Scores; k: "csi" | "pod" | "far"; bold?: boolean }) {
  return <td className={`${td} ${bold ? "font-semibold text-slate-50" : ""}`}>{pct(s[k])}</td>;
}
