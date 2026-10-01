"use client";

import { useCallback, useEffect, useState } from "react";
import { adminGet } from "@/lib/admin";

/** Loads an admin endpoint and refreshes it periodically. */
export function useAdminData<T>(path: string, refreshMs = 60_000) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const reload = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const d = await adminGet<T>(path);
        if (cancelled) return;
        setData(d);
        setError(null);
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      }
    };
    load();
    const id = setInterval(load, refreshMs);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [path, refreshMs, tick]);

  return { data, error, reload };
}

export function Panel({ title, action, children }: { title: string; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="rounded-2xl border border-slate-800 bg-slate-900/40 p-5">
      <div className="mb-4 flex items-center justify-between gap-4">
        <h2 className="text-sm font-semibold text-slate-200">{title}</h2>
        {action}
      </div>
      {children}
    </section>
  );
}

export function Stat({ label, value, sub }: { label: string; value: React.ReactNode; sub?: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
      <p className="text-xs text-slate-500">{label}</p>
      <p className="mt-1 text-2xl font-semibold tabular-nums text-slate-50">{value}</p>
      {sub && <p className="mt-0.5 text-xs text-slate-500">{sub}</p>}
    </div>
  );
}

export function Loading({ error }: { error: string | null }) {
  return error ? (
    <p className="text-sm text-amber-300">Lỗi: {error}</p>
  ) : (
    <p className="animate-pulse text-sm text-slate-500">Đang tải…</p>
  );
}

/** Hit / miss marker that never relies on color alone. */
export function Verdict({ ok }: { ok: boolean }) {
  return (
    <span className="inline-flex items-center gap-1 text-slate-300">
      <span aria-hidden className={ok ? "text-[#0ca30c]" : "text-[#d03b3b]"}>
        {ok ? "✓" : "✗"}
      </span>
      {ok ? "đúng" : "sai"}
    </span>
  );
}

export const th = "py-2 pr-4 text-left text-xs font-medium text-slate-500";
export const td = "py-2 pr-4 tabular-nums text-slate-300";
