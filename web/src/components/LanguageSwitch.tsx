"use client";

import { useEffect } from "react";
import { useLocale, type Locale } from "@/lib/i18n";

const OPTIONS: { value: Locale; label: string }[] = [
  { value: "vi", label: "VI" },
  { value: "en", label: "EN" },
];

/**
 * VI | EN toggle, pinned to the top-right corner of the page. Also keeps <html lang>, the tab title and (on the main
 * page) the description in step, since the prerendered page is always English.
 */
export function LanguageSwitch({ title }: { title: "title" | "adminTitle" }) {
  const { locale, setLocale, t } = useLocale();
  const docTitle = t.meta[title];
  const description = title === "title" ? t.meta.description : null;

  useEffect(() => {
    document.documentElement.lang = locale;
    // Next writes the (English) metadata after hydration, so reapply ours
    // whenever <head> changes.
    const apply = () => {
      if (document.title !== docTitle) document.title = docTitle;
      if (description === null) return;
      const meta = document.querySelector('meta[name="description"]');
      if (meta && meta.getAttribute("content") !== description) meta.setAttribute("content", description);
    };
    apply();
    const obs = new MutationObserver(apply);
    obs.observe(document.head, { subtree: true, childList: true, characterData: true });
    return () => obs.disconnect();
  }, [locale, docTitle, description]);

  return (
    <div className="absolute right-4 top-4 z-20 flex gap-1 rounded-lg bg-slate-950/90 p-1 text-xs shadow-lg" role="group" aria-label={t.language.label}>
      {OPTIONS.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => setLocale(o.value)}
          aria-pressed={locale === o.value}
          className={`cursor-pointer rounded-md px-2 py-1 ${
            locale === o.value ? "bg-slate-800 text-slate-50" : "text-slate-500 hover:text-slate-300"
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
