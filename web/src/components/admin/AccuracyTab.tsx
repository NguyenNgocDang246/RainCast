"use client";

import { useState } from "react";
import type { Accuracy, Scores } from "@/lib/admin";
import { pct } from "@/lib/adminFormat";
import { BacktestPanel } from "./BacktestPanel";
import { Loading, Panel, Stat, td, th, useAdminData } from "./ui";

// Categorical slots 1-3 (dark), validated against the slate-900 surface.
export const SERIES = {
  model: { label: "Mô hình", color: "#3987e5" },
  persistence: { label: "Giữ nguyên (baseline)", color: "#d95926" },
  trend: { label: "Mô hình + xu hướng", color: "#199e70" },
} as const;
export type SeriesKey = keyof typeof SERIES;

export const METRICS = [
  { key: "csi", label: "CSI" },
  { key: "accuracy", label: "Chính xác" },
  { key: "pod", label: "Bắt được mưa" },
  { key: "far", label: "Báo động giả" },
] as const;
export type Metric = (typeof METRICS)[number]["key"];

/** One lead time with the scores of each plotted series. */
export type ChartRow = { lead_min: number; scores: Partial<Record<SeriesKey, Scores>> };

export function AccuracyTab() {
  const { data, error } = useAdminData<Accuracy>("/api/admin/accuracy");
  const [metric, setMetric] = useState<Metric>("csi");
  if (!data) return <Loading error={error} />;
  const { arrival, shadow } = data;

  // With trend data, compare all three on the same forecasts; otherwise
  // show the model against the baseline on everything.
  const series: SeriesKey[] = shadow ? ["model", "trend", "persistence"] : ["model", "persistence"];
  const rows: ChartRow[] = shadow
    ? shadow.leads.map((l) => ({ lead_min: l.lead_min, scores: { model: l.model, trend: l.trend, persistence: l.persistence } }))
    : data.leads.map((l) => ({ lead_min: l.lead_min, scores: { model: l.model, persistence: l.persistence } }));
  const head = shadow ?? data;

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat label="CSI · mô hình" value={pct(head.model.csi)} sub={`chính xác ${pct(head.model.accuracy)}`} />
        <Stat
          label="CSI · mô hình + xu hướng"
          value={shadow ? pct(shadow.trend.csi) : "–"}
          sub={shadow ? `chính xác ${pct(shadow.trend.accuracy)}` : "chưa có dữ liệu"}
        />
        <Stat label="CSI · baseline" value={pct(head.persistence.csi)} sub={`chính xác ${pct(head.persistence.accuracy)}`} />
        <Stat
          label="Sai số giờ mưa tới"
          value={arrival.mae_min == null ? "–" : `±${arrival.mae_min.toFixed(0)} ph`}
          sub={
            arrival.bias_min == null
              ? "chưa có lần mưa tới nào"
              : `${arrival.n} lần · ${arrival.bias_min > 0 ? "mưa tới trễ hơn" : "mưa tới sớm hơn"} dự báo ${Math.abs(arrival.bias_min).toFixed(0)} ph`
          }
        />
      </div>

      <Panel
        title={
          shadow
            ? `So sánh theo mốc · ${shadow.model.n} dự đoán có cả hai phiên bản`
            : `Theo mốc dự báo · ${data.model.n} dự đoán`
        }
        action={<MetricPicker value={metric} onChange={setMetric} />}
      >
        {rows.length ? (
          <LeadChart rows={rows} series={series} metric={metric} />
        ) : (
          <p className="text-sm text-slate-500">Chưa có dự báo nào đủ thời gian để đối chiếu.</p>
        )}
        <p className="mt-3 text-xs leading-relaxed text-slate-500">
          Dữ liệu thật từ {data.issued} lần dự báo của các trạm, chấm khi khung radar thật về tới. “Xu hướng” cho vùng mưa
          mạnh lên hoặc yếu đi theo đà 20 phút gần nhất; nó chạy song song để so sánh, người dùng chưa thấy.
        </p>
      </Panel>

      <Panel title={`Bảng chi tiết · toàn bộ ${data.verified} dự đoán đã đối chiếu`}>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] text-sm">
            <thead>
              <tr className="border-b border-slate-800">
                <th className={th}>Mốc</th>
                <th className={th}>Số lần</th>
                <th className={th}>CSI</th>
                <th className={th}>Chính xác</th>
                <th className={th}>Bắt được mưa (POD)</th>
                <th className={th}>Báo động giả (FAR)</th>
                <th className={th}>Trúng / trượt / giả / đúng-không-mưa</th>
                <th className={th}>Sai số dBZ</th>
              </tr>
            </thead>
            <tbody>
              {data.leads.map((l) => (
                <tr key={l.lead_min} className="border-b border-slate-800/60">
                  <td className={`${td} text-slate-100`}>{l.lead_min} phút</td>
                  <td className={td}>{l.model.n}</td>
                  <Cmp m={l.model.csi} b={l.persistence.csi} />
                  <Cmp m={l.model.accuracy} b={l.persistence.accuracy} />
                  <Cmp m={l.model.pod} b={l.persistence.pod} />
                  <Cmp m={l.model.far} b={l.persistence.far} />
                  <td className={td}>{contingency(l.model)}</td>
                  <td className={td}>{l.mae_dbz == null ? "–" : l.mae_dbz.toFixed(1)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="mt-3 text-xs text-slate-500">
          Số nhỏ là baseline “trời giữ nguyên”. CSI = trúng / (trúng + trượt + giả), chỉ số chính để tối ưu.
        </p>
      </Panel>

      <BacktestPanel />
    </div>
  );
}

export function MetricPicker({ value, onChange }: { value: Metric; onChange: (m: Metric) => void }) {
  return (
    <div className="flex flex-wrap gap-1" role="group" aria-label="Chỉ số">
      {METRICS.map((m) => (
        <button
          key={m.key}
          type="button"
          onClick={() => onChange(m.key)}
          aria-pressed={value === m.key}
          className={`cursor-pointer rounded-lg px-3 py-1 text-xs ${
            value === m.key ? "bg-slate-700 text-slate-50" : "text-slate-400 hover:text-slate-200"
          }`}
        >
          {m.label}
        </button>
      ))}
    </div>
  );
}

export const contingency = (s: Scores) => `${s.hits} / ${s.misses} / ${s.false_alarms} / ${s.correct_negatives}`;

function Cmp({ m, b }: { m: number | null; b: number | null }) {
  return (
    <td className={td}>
      <span className="text-slate-100">{pct(m)}</span>
      <span className="ml-1.5 text-xs text-slate-500">{pct(b)}</span>
    </td>
  );
}

const W = 640;
const H = 240;
const PAD = { top: 12, right: 8, bottom: 28, left: 40 };

/** Grouped bars: one bar per series at each lead time. */
export function LeadChart({ rows, series, metric }: { rows: ChartRow[]; series: SeriesKey[]; metric: Metric }) {
  const [hover, setHover] = useState<{ lead: number; key: SeriesKey; x: number; y: number } | null>(null);
  const iw = W - PAD.left - PAD.right;
  const ih = H - PAD.top - PAD.bottom;
  const band = iw / rows.length;
  const gap = 2;
  const barW = Math.min(24, (band - 16 - gap * (series.length - 1)) / series.length);
  const groupW = barW * series.length + gap * (series.length - 1);
  const y = (v: number) => PAD.top + ih - v * ih;

  return (
    <figure className="relative">
      <div className="mb-3 flex flex-wrap gap-4 text-xs text-slate-300">
        {series.map((k) => (
          <span key={k} className="inline-flex items-center gap-1.5">
            <span className="size-2.5 rounded-sm" style={{ background: SERIES[k].color }} />
            {SERIES[k].label}
          </span>
        ))}
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" aria-label="Chỉ số theo mốc dự báo">
        {[0, 0.25, 0.5, 0.75, 1].map((v) => (
          <g key={v}>
            <line x1={PAD.left} x2={W - PAD.right} y1={y(v)} y2={y(v)} stroke="#1e293b" />
            <text x={PAD.left - 6} y={y(v) + 4} textAnchor="end" className="fill-slate-500 text-[10px]">
              {v * 100}%
            </text>
          </g>
        ))}
        {rows.map((r, i) => {
          const cx = PAD.left + band * i + band / 2;
          return (
            <g key={r.lead_min}>
              {series.map((k, j) => {
                const v = r.scores[k]?.[metric];
                if (v == null) return null;
                const x = cx - groupW / 2 + j * (barW + gap);
                const top = y(v);
                const h = Math.max(PAD.top + ih - top, 0);
                const rad = Math.min(4, h, barW / 2);
                return (
                  <path
                    key={k}
                    // Rounded data end, square at the baseline.
                    d={`M${x},${PAD.top + ih} V${top + rad} Q${x},${top} ${x + rad},${top} H${x + barW - rad} Q${x + barW},${top} ${x + barW},${top + rad} V${PAD.top + ih} Z`}
                    fill={SERIES[k].color}
                    opacity={hover && (hover.lead !== r.lead_min || hover.key !== k) ? 0.45 : 1}
                    onMouseEnter={() => setHover({ lead: r.lead_min, key: k, x: x + barW / 2, y: top })}
                    onMouseLeave={() => setHover(null)}
                  />
                );
              })}
              <text x={cx} y={H - 10} textAnchor="middle" className="fill-slate-500 text-[10px]">
                +{r.lead_min}′
              </text>
            </g>
          );
        })}
      </svg>
      {hover && (
        <Tooltip
          row={rows.find((r) => r.lead_min === hover.lead)!}
          seriesKey={hover.key}
          metric={metric}
          style={{ left: `${(hover.x / W) * 100}%`, top: `${(hover.y / H) * 100}%` }}
        />
      )}
    </figure>
  );
}

function Tooltip({
  row,
  seriesKey,
  metric,
  style,
}: {
  row: ChartRow;
  seriesKey: SeriesKey;
  metric: Metric;
  style: React.CSSProperties;
}) {
  const s = SERIES[seriesKey];
  const sc = row.scores[seriesKey]!;
  return (
    <div
      className="pointer-events-none absolute z-10 -translate-x-1/2 -translate-y-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-xs shadow-xl"
      style={{ ...style, marginTop: 24 }}
    >
      <p className="flex items-center gap-1.5 text-slate-300">
        <span className="size-2 rounded-sm" style={{ background: s.color }} />
        {s.label} · +{row.lead_min} phút
      </p>
      <p className="mt-1 text-base font-semibold text-slate-50">
        {pct(sc[metric])} <span className="text-xs font-normal text-slate-400">{METRICS.find((m) => m.key === metric)!.label}</span>
      </p>
      <p className="text-slate-400">
        {sc.n} lần · trúng/trượt/giả/đúng: {contingency(sc)}
      </p>
    </div>
  );
}
