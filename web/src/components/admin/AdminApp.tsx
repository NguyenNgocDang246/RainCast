"use client";

import { useState } from "react";
import { useLocale } from "@/lib/i18n";
import { LanguageSwitch } from "../LanguageSwitch";
import { AccuracyTab } from "./AccuracyTab";
import { FramesTab, IssuesTab, LookupsTab } from "./HistoryTabs";
import { OverviewTab } from "./OverviewTab";
import { ToolsTab } from "./ToolsTab";

const TABS = ["overview", "accuracy", "issues", "lookups", "frames", "tools"] as const;
type Tab = (typeof TABS)[number];

export function AdminApp() {
  const { t } = useLocale();
  const [tab, setTab] = useState<Tab>("overview");
  const [inspect, setInspect] = useState<{ lat: number; lon: number } | null>(null);

  return (
    <main className="mx-auto w-full max-w-6xl px-4 py-8">
      <LanguageSwitch title="adminTitle" />
      <h1 className="mb-6 text-lg font-semibold text-slate-100">Raincast · Admin</h1>

      <nav className="mb-6 flex flex-wrap gap-1 border-b border-slate-800" role="tablist">
        {TABS.map((key) => (
          <button
            key={key}
            type="button"
            role="tab"
            aria-selected={tab === key}
            onClick={() => setTab(key)}
            className={`-mb-px cursor-pointer border-b-2 px-3 py-2 text-sm ${
              tab === key ? "border-sky-400 text-slate-50" : "border-transparent text-slate-400 hover:text-slate-200"
            }`}
          >
            {t.admin.tabs[key]}
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
