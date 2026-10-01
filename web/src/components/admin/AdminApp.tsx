"use client";

import { useState } from "react";
import { AccuracyTab } from "./AccuracyTab";
import { FramesTab, IssuesTab, LookupsTab } from "./HistoryTabs";
import { OverviewTab } from "./OverviewTab";
import { ToolsTab } from "./ToolsTab";

const TABS = [
  { key: "overview", label: "Tổng quan" },
  { key: "accuracy", label: "Độ chính xác" },
  { key: "issues", label: "Lịch sử dự báo" },
  { key: "lookups", label: "Lượt tra cứu" },
  { key: "frames", label: "Khung radar" },
  { key: "tools", label: "Công cụ" },
] as const;
type Tab = (typeof TABS)[number]["key"];

export function AdminApp() {
  const [tab, setTab] = useState<Tab>("overview");
  const [inspect, setInspect] = useState<{ lat: number; lon: number } | null>(null);

  return (
    <main className="mx-auto w-full max-w-6xl px-4 py-8">
      <h1 className="mb-6 text-lg font-semibold text-slate-100">Raincast · Admin</h1>

      <nav className="mb-6 flex flex-wrap gap-1 border-b border-slate-800" role="tablist">
        {TABS.map((t) => (
          <button
            key={t.key}
            type="button"
            role="tab"
            aria-selected={tab === t.key}
            onClick={() => setTab(t.key)}
            className={`-mb-px cursor-pointer border-b-2 px-3 py-2 text-sm ${
              tab === t.key ? "border-sky-400 text-slate-50" : "border-transparent text-slate-400 hover:text-slate-200"
            }`}
          >
            {t.label}
          </button>
        ))}
      </nav>

      {tab === "overview" && <OverviewTab />}
      {tab === "accuracy" && <AccuracyTab />}
      {tab === "issues" && <IssuesTab />}
      {tab === "lookups" && (
        <LookupsTab
          onInspect={(lat, lon) => {
            setInspect({ lat, lon });
            setTab("tools");
          }}
        />
      )}
      {tab === "frames" && <FramesTab />}
      {tab === "tools" && <ToolsTab key={inspect ? `${inspect.lat},${inspect.lon}` : "home"} initial={inspect} />}
    </main>
  );
}
