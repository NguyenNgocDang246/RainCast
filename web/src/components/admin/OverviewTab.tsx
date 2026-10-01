"use client";

import type { Overview } from "@/lib/admin";
import { ago, dateTime } from "@/lib/adminFormat";
import { Loading, Panel, Stat, useAdminData } from "./ui";

export function OverviewTab() {
  const { data, error } = useAdminData<Overview>("/api/admin/overview", 30_000);
  if (!data) return <Loading error={error} />;
  const { pipeline: p, counts: c, geocoding: g } = data;
  const now = new Date(data.server_time).getTime();

  return (
    <div className="space-y-6">
      <Panel title="Pipeline radar">
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <Stat label="Khung radar mới nhất" value={dateTime(p.latest_frame)} sub={ago(p.latest_frame, now)} />
          <Stat label="Lần poll gần nhất" value={ago(p.last_tick, now)} sub={`${p.ticks} lần kể từ khi chạy`} />
          <Stat label="Khung trong feed" value={p.frames_in_feed} sub="RainViewer giữ ~2 giờ" />
          <Stat label="Vùng đang cache" value={p.cached_regions} sub="cho dự báo theo yêu cầu" />
        </div>
        <p className="mt-4 text-xs text-slate-500">
          {p.stations.length} trạm thu thập dữ liệu chấm điểm: {p.stations.map((s) => s.name).join(", ")} · chạy từ{" "}
          {dateTime(p.started)}
        </p>
        {p.last_error && (
          <p className="mt-3 rounded-lg border border-[#d03b3b]/40 bg-[#d03b3b]/10 px-3 py-2 text-sm text-[#f07a7a]">
            <span aria-hidden>⚠ </span>Lỗi lần poll gần nhất: {p.last_error}
          </p>
        )}
      </Panel>

      <Panel title="Dữ liệu đã lưu">
        <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
          <Stat label="Khung radar" value={c.frames} sub={`đo tại ${c.stations} trạm`} />
          <Stat label="Lần dự báo (điểm theo dõi)" value={c.issues} />
          <Stat label="Dự đoán theo mốc" value={c.forecasts} />
          <Stat label="Đã đối chiếu" value={c.verified} />
          <Stat label="Lượt tra cứu của người dùng" value={c.lookups} />
        </div>
      </Panel>

      <Panel title={`Geocoding (từ lần khởi động) · LocationIQ ${g.locationiq_enabled ? "bật" : "tắt"}`}>
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <Stat label="Trả từ cache" value={g.cache_hits} />
          <Stat
            label="LocationIQ"
            value={g.locationiq_ok}
            sub={`${g.locationiq_rate_limited} lần hết lượt · ${g.locationiq_errors} lỗi`}
          />
          <Stat label="Photon" value={g.photon_ok} sub={`${g.photon_errors} lỗi`} />
          <Stat label="Nominatim" value={g.nominatim_ok} sub={`${g.nominatim_errors} lỗi`} />
        </div>
      </Panel>
    </div>
  );
}
