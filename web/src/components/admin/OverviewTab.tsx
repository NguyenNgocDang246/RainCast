"use client";

import type { Overview } from "@/lib/admin";
import { ago, dateTime } from "@/lib/adminFormat";
import { useLocale } from "@/lib/i18n";
import { Loading, Panel, Stat, useAdminData } from "./ui";

export function OverviewTab() {
  const { locale, t } = useLocale();
  const { data, error } = useAdminData<Overview>("/api/admin/overview", 30_000);
  if (!data) return <Loading error={error} />;
  const { pipeline: p, counts: c, geocoding: g } = data;
  const now = new Date(data.server_time).getTime();
  const o = t.admin.overview;

  return (
    <div className="space-y-6">
      <Panel title={o.pipeline}>
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <Stat label={o.latestFrame} value={dateTime(p.latest_frame, locale)} sub={ago(p.latest_frame, now, locale)} />
          <Stat label={o.lastPoll} value={ago(p.last_tick, now, locale)} sub={o.ticks(p.ticks)} />
          <Stat label={o.framesInFeed} value={p.frames_in_feed} sub={o.feedKeeps} />
          <Stat label={o.cachedRegions} value={p.cached_regions} sub={o.forOnDemand} />
        </div>
        <p className="mt-4 text-xs text-slate-500">
          {o.stations(
            p.stations.length,
            p.stations.map((s) => s.name).join(", "),
            dateTime(p.started, locale),
          )}
        </p>
        {p.last_error && (
          <p className="mt-3 rounded-lg border border-[#d03b3b]/40 bg-[#d03b3b]/10 px-3 py-2 text-sm text-[#f07a7a]">
            <span aria-hidden>⚠ </span>
            {o.lastError} {p.last_error}
          </p>
        )}
      </Panel>

      <Panel title={o.stored}>
        <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
          <Stat label={o.frames} value={c.frames} sub={o.atStations(c.stations)} />
          <Stat label={o.issues} value={c.issues} />
          <Stat label={o.forecasts} value={c.forecasts} />
          <Stat label={o.verified} value={c.verified} />
          <Stat label={o.lookups} value={c.lookups} />
        </div>
      </Panel>

      <Panel title={o.geocoding(g.geoapify_enabled)}>
        <div className="grid grid-cols-2 gap-3 md:grid-cols-6">
          <Stat label={o.cacheHits} value={g.cache_hits} />
          <Stat label="Autocomplete" value={g.autocomplete_ok} />
          <Stat label="Search" value={g.search_ok} />
          <Stat label="Reverse" value={g.reverse_ok} />
          <Stat label={o.failed} value={g.errors} sub={o.rateLimited(g.rate_limited)} />
          <Stat label={o.tiles} value={g.tile_fetched} sub={o.tilesSub(g.tile_cache_hits, g.tile_errors)} />
        </div>
      </Panel>
    </div>
  );
}
