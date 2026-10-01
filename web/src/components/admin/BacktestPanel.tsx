"use client";

import { useState } from "react";
import type { BacktestReport, Scores } from "@/lib/admin";
import { adminPost } from "@/lib/admin";
import { dateTime, pct } from "@/lib/adminFormat";
import { Panel, td, th, useAdminData } from "./ui";

/**
 * Re-scores every stored radar frame with several model settings, on ~1,000
 * points around the primary station, so settings can be compared on far
 * more cases than the stations alone provide.
 */
export function BacktestPanel() {
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
  // The best overall CSI among model variants (the baseline is row 0).
  const best = results.slice(1).reduce<number | null>((m, r) => Math.max(m ?? 0, r.overall.csi ?? 0), null);

  return (
    <Panel
      title="Backtest trên dữ liệu radar đã lưu"
      action={
        <button
          type="button"
          onClick={run}
          disabled={running}
          className="cursor-pointer rounded-lg bg-sky-500 px-3 py-1.5 text-xs font-medium text-slate-950 hover:bg-sky-400 disabled:cursor-wait disabled:opacity-60"
        >
          {running ? "Đang chạy…" : "Chạy backtest"}
        </button>
      }
    >
      {(runError || error) && <p className="mb-3 text-sm text-amber-300">Lỗi: {runError ?? error}</p>}
      {!report ? (
        <p className="text-sm text-slate-500">Chưa chạy lần nào. Bấm “Chạy backtest” để chấm lại toàn bộ khung đã lưu.</p>
      ) : report.issues === 0 ? (
        <p className="text-sm text-slate-500">
          Chưa đủ dữ liệu: cần ít nhất 9 khung liên tiếp trước và 6 khung sau một thời điểm (đã có {report.frames} khung).
        </p>
      ) : (
        <>
          <p className="mb-4 text-xs text-slate-500">
            {report.issues} thời điểm dự báo ({dateTime(new Date(report.from * 1000).toISOString())} →{" "}
            {dateTime(new Date(report.to * 1000).toISOString())}) × {report.points} điểm quanh trạm chính · {report.frames}{" "}
            khung · chạy lúc {dateTime(report.generated_at)} ({report.duration})
          </p>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead>
                <tr className="border-b border-slate-800">
                  <th className={th}>Cấu hình</th>
                  {leads.map((l) => (
                    <th key={l} className={th}>
                      CSI +{l}′
                    </th>
                  ))}
                  <th className={th}>CSI tổng</th>
                  <th className={th}>Bắt được mưa</th>
                  <th className={th}>Báo động giả</th>
                </tr>
              </thead>
              <tbody>
                {results.map((r, i) => {
                  const top = i > 0 && best != null && (r.overall.csi ?? 0) >= best;
                  return (
                    <tr key={r.name} className="border-b border-slate-800/60">
                      <td className={`${td} ${top ? "font-semibold text-slate-50" : "text-slate-100"}`}>
                        {r.name}
                        {top && <span className="ml-2 text-xs font-normal text-sky-400">tốt nhất</span>}
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
            “N cặp” là số cặp khung (10 phút) dùng để ước lượng chuyển động; “+ xu hướng” thêm mưa mạnh lên/yếu đi. Mọi cấu
            hình chấm trên cùng các thời điểm và điểm. Dữ liệu tile được giữ 7 ngày.
          </p>
        </>
      )}
    </Panel>
  );
}

function Strong({ s, k, bold }: { s: Scores; k: "csi" | "pod" | "far"; bold?: boolean }) {
  return <td className={`${td} ${bold ? "font-semibold text-slate-50" : ""}`}>{pct(s[k])}</td>;
}
