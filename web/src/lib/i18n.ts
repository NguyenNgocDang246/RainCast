import { useSyncExternalStore } from "react";
import { en } from "./messages/en";
import { vi, type Dict } from "./messages/vi";

export type Locale = "vi" | "en";

export const messages: Record<Locale, Dict> = { vi, en };

const STORAGE_KEY = "raincast.locale";

/** BCP 47 tag for Intl date and time formatting. */
export const bcp47 = (l: Locale) => (l === "vi" ? "vi-VN" : "en-GB");

// The language lives in localStorage, read through useSyncExternalStore like
// the chosen place: prerendered HTML is Vietnamese, the client switches after
// hydration.
const listeners = new Set<() => void>();

function subscribe(cb: () => void) {
  listeners.add(cb);
  window.addEventListener("storage", cb);
  return () => {
    listeners.delete(cb);
    window.removeEventListener("storage", cb);
  };
}

function read(): Locale {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved === "vi" || saved === "en") return saved;
  } catch {
    // Storage unavailable; fall back to the browser language.
  }
  return navigator.language.toLowerCase().startsWith("vi") ? "vi" : "en";
}

/** A backend error message in the chosen language, when it is a known one. */
export const errorText = (t: Dict, msg: string) => t.errors[msg] ?? msg;

export function setLocale(l: Locale) {
  try {
    localStorage.setItem(STORAGE_KEY, l);
  } catch {
    // Storage unavailable (private mode); the choice just won't persist.
  }
  listeners.forEach((cb) => cb());
}

export function useLocale() {
  const locale = useSyncExternalStore(subscribe, read, () => "vi" as Locale);
  return { locale, setLocale, t: messages[locale] };
}
