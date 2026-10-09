import { useSyncExternalStore } from "react";
import { en } from "./messages/en";
import { vi, type Dict } from "./messages/vi";

export type Locale = "vi" | "en";

export const messages: Record<Locale, Dict> = { vi, en };

const STORAGE_KEY = "raincast.locale";

/** BCP 47 tag for Intl date and time formatting. */
export const bcp47 = (l: Locale) => (l === "vi" ? "vi-VN" : "en-GB");

// The language lives in localStorage, read through useSyncExternalStore like
// the chosen place: prerendered HTML is English, the client switches after
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
    // Storage unavailable; use the default.
  }
  return "en";
}

/** A backend error message in the chosen language, when it is a known one; as is otherwise (admin pages). */
export const errorText = (t: Dict, msg: string) => t.errors[msg] ?? msg;

/**
 * An error as visitors see it: a known one translated, and anything else (a
 * crash, a proxy's error page, a rate limit) as a plain line without codes.
 */
export function userError(t: Dict, msg: string): string {
  const known = t.errors[msg];
  if (known) return known;
  // Rate limits and an overloaded or starting server pass with a retry.
  if (/\b(429|503)\b/.test(msg)) return t.problem.busy;
  return t.problem.generic;
}

export function setLocale(l: Locale) {
  try {
    localStorage.setItem(STORAGE_KEY, l);
  } catch {
    // Storage unavailable (private mode); the choice just won't persist.
  }
  listeners.forEach((cb) => cb());
}

export function useLocale() {
  const locale = useSyncExternalStore(subscribe, read, () => "en" as Locale);
  return { locale, setLocale, t: messages[locale] };
}
