"use client";

import type { Frame } from "@/lib/admin";
import { dateTime } from "@/lib/adminFormat";
import { useLocale } from "@/lib/i18n";
import { Loading, Panel, td, th, useAdminData } from "./ui";

/** Frames cmd/collect recorded, from its database. */
export function FramesTab() {
  const { locale, t } = useLocale();
  const h = t.admin.history;
  const { data, error } = useAdminData<Frame[]>("/api/admin/frames?limit=100");
  if (!data) return <Loading error={error} />;
  return (
    <Panel title={h.framesTitle(data.length)}>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-slate-800">
              <th className={th}>{h.frameTime}</th>
              <th className={th}>{h.path}</th>
            </tr>
          </thead>
          <tbody>
            {data.map((f) => (
              <tr key={f.time} className="border-b border-slate-800/60">
                <td className={`${td} text-slate-100`}>{dateTime(new Date(f.time * 1000).toISOString(), locale)}</td>
                <td className={`${td} font-mono text-xs`}>{f.path}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {data.length === 0 && <p className="mt-3 text-sm text-slate-500">{h.noFrames}</p>}
    </Panel>
  );
}
