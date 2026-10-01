"use client";

import { useEffect, useId, useState } from "react";
import { geocode, suggest, type Place } from "@/lib/api";
import { errorText, useLocale } from "@/lib/i18n";
import { addRecent, clearRecent, loadRecent } from "@/lib/recent";

type Props = {
  onSelect: (place: Place) => void;
  /** Suggestions near this point rank first (usually the current place). */
  near: Place | null;
};

const DEBOUNCE_MS = 300;
const MIN_CHARS = 2;

/** Links and coordinates are resolved on submit, not suggested. */
const isLinkOrCoords = (q: string) =>
  /https?:\/\//.test(q) || /^-?\d+(\.\d+)?\s*[,;\s]\s*-?\d+(\.\d+)?$/.test(q);

/** Results tagged with the query that produced them, so stale ones never show. */
type Results = { query: string; places: Place[] };

/** A dropdown row: a search match, or a place picked before. */
type Item = { kind: "place" | "recent"; place: Place };

export function LocationSearch({ onSelect, near }: Props) {
  const { t } = useLocale();
  const listId = useId();
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<Results | null>(null);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Read on the client only; the list is hidden until focus, so the
  // prerendered markup matches.
  const [recent, setRecent] = useState<Place[]>(() =>
    typeof window === "undefined" ? [] : loadRecent(),
  );

  const q = query.trim();
  const places = results && results.query === q ? results.places : [];
  // Empty input shows recent searches; typing shows matches.
  const items: Item[] = q
    ? places.map((place) => ({ kind: "place", place }))
    : recent.map((place) => ({ kind: "recent", place }));
  const showList = open && items.length > 0;

  // Suggest while typing, debounced; a newer keystroke aborts the older request.
  useEffect(() => {
    if (q.length < MIN_CHARS || isLinkOrCoords(q)) return;
    const ctrl = new AbortController();
    const timer = setTimeout(async () => {
      try {
        const found = await suggest(q, near, ctrl.signal);
        setResults({ query: q, places: found });
        setActive(-1);
      } catch {
        // Suggestions are best-effort; submit still works.
      }
    }, DEBOUNCE_MS);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  }, [q, near]);

  const choose = (p: Place) => {
    setRecent(addRecent(p));
    setResults(null);
    setQuery("");
    setOpen(false);
    setError(null);
    onSelect(p);
  };

  /** Looks text up afresh; one match is chosen, several are listed. */
  const search = async (text: string) => {
    if (!text || busy) return;
    setBusy(true);
    setError(null);
    try {
      const found = await geocode(text);
      // The address providers only know OpenStreetMap; a Google Maps link
      // carries its own coordinates, so it works for places OSM lacks.
      if (found.length === 0)
        setError(t.search.notFound(text));
      else if (found.length === 1) choose(found[0]);
      else {
        setResults({ query: text, places: found });
        setActive(-1);
        setOpen(true);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  // A recent place is reused as-is: searching its name again could land
  // somewhere else with a similar name.
  const pick = (item: Item) => choose(item.place);

  const submit = (e?: React.SubmitEvent<HTMLFormElement>) => {
    e?.preventDefault();
    if (showList && active >= 0) return pick(items[active]);
    search(q);
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      if (!items.length) return;
      e.preventDefault();
      setOpen(true);
      const step = e.key === "ArrowDown" ? 1 : -1;
      // Cycle through -1 (the input itself) .. n-1.
      const n = items.length + 1;
      setActive((i) => ((i + 1 + step + n) % n) - 1);
    } else if (e.key === "Escape") {
      setOpen(false);
      setActive(-1);
    }
  };

  return (
    <div className="relative w-full max-w-xl">
      <form onSubmit={submit} className="flex gap-2" role="search">
        <input
          type="search"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setOpen(true);
            setActive(-1);
            setError(null);
          }}
          onFocus={() => setOpen(true)}
          // The input keeps focus after a pick, so focus alone won't reopen.
          onClick={() => setOpen(true)}
          onBlur={() => setOpen(false)}
          onKeyDown={onKeyDown}
          placeholder={t.search.placeholder}
          role="combobox"
          aria-label={t.search.label}
          aria-autocomplete="list"
          aria-expanded={showList}
          aria-controls={listId}
          aria-activedescendant={showList && active >= 0 ? `${listId}-${active}` : undefined}
          autoComplete="off"
          className="min-w-0 flex-1 rounded-xl border border-slate-800 bg-slate-900 px-4 py-3 text-slate-100 placeholder:text-slate-500 focus:border-sky-500 focus:outline-none"
        />
        <button
          type="submit"
          disabled={busy || !q}
          className="rounded-xl bg-sky-500 px-5 py-3 font-medium text-slate-950 transition hover:bg-sky-400 disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
        >
          {busy ? t.search.busy : t.search.submit}
        </button>
      </form>

      {error && <p className="mt-2 text-sm text-amber-300">{errorText(t, error)}</p>}

      {showList && (
        <div className="absolute inset-x-0 top-full z-10 mt-2 overflow-hidden rounded-xl border border-slate-800 bg-slate-900 shadow-xl">
          {!q && (
            <div className="flex items-center justify-between px-4 pt-3 pb-1 text-xs text-slate-500">
              <span>{t.search.recent}</span>
              <button
                type="button"
                // mousedown keeps focus in the input so the list stays open.
                onMouseDown={(e) => {
                  e.preventDefault();
                  setRecent(clearRecent());
                }}
                className="cursor-pointer hover:text-slate-300"
              >
                {t.search.clear}
              </button>
            </div>
          )}
          <ul id={listId} role="listbox">
            {items.map((item, i) => (
              <li
                key={`${item.kind}:${item.place.lat},${item.place.lon},${item.place.name}`}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={i === active}
                // mousedown fires before the input's blur closes the list.
                onMouseDown={(e) => {
                  e.preventDefault();
                  pick(item);
                }}
                onMouseEnter={() => setActive(i)}
                className={`cursor-pointer px-4 py-3 ${i === active ? "bg-slate-800" : ""}`}
              >
                <span className="flex items-start gap-2">
                  {item.kind === "recent" && (
                    <svg
                      viewBox="0 0 20 20"
                      className="mt-1 size-4 shrink-0 fill-slate-500"
                      aria-hidden="true"
                    >
                      <path d="M10 2a8 8 0 1 0 8 8h-2a6 6 0 1 1-1.76-4.24L12 8h6V2l-2.35 2.35A7.97 7.97 0 0 0 10 2Zm-1 4v5l4 2.5.75-1.23L10.5 10.2V6H9Z" />
                    </svg>
                  )}
                  <span className="min-w-0">
                    <span className="block text-slate-100">{item.place.name}</span>
                    {item.place.address && item.place.address !== item.place.name && (
                      <span className="mt-0.5 block truncate text-xs text-slate-500">
                        {item.place.address}
                      </span>
                    )}
                  </span>
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
