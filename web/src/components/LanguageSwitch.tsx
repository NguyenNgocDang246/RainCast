"use client";

import { useEffect } from "react";
import { useLocale, type Locale } from "@/lib/i18n";

const OPTIONS: { value: Locale; label: string }[] = [
  { value: "vi", label: "VI" },
  { value: "en", label: "EN" },
];

/**
 * VI | EN toggle, pinned to the top-right corner of the page. Also keeps <html lang> and the tab title in step, since
 * the prerendered page is always Vietnamese.
 */
export function LanguageSwitch({ title }: { title: "title" | "adminTitle" }) {
  const { locale, setLocale, t } = useLocale();
  const docTitle = t.meta[title];

  useEffect(() => {
    document.documentElement.lang = locale;
    // Next writes the (Vietnamese) metadata title after hydration, so
    // reapply ours whenever <head> changes.
    const apply = () => {
      if (document.title !== docTitle) document.title = docTitle;
    };
    apply();
    const obs = new MutationObserver(apply);
    obs.observe(document.head, { subtree: true, childList: true, characterData: true });
    return () => obs.disconnect();
  }, [locale, docTitle]);

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
