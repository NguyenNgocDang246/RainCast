type IconProps = { className?: string };

/** Raincast mark: a raindrop with a highlight. */
export function Logo({ className }: IconProps) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true">
      <defs>
        <linearGradient id="raincast-drop" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="#7dd3fc" />
          <stop offset="1" stopColor="#0284c7" />
        </linearGradient>
      </defs>
      <path d="M16 3c-4.6 6.3-9 11.7-9 16.4A9 9 0 0 0 25 19.4C25 14.7 20.6 9.3 16 3Z" fill="url(#raincast-drop)" />
      <path d="M12 19.5a4.5 4.5 0 0 0 3.5 4.4" fill="none" stroke="#e0f2fe" strokeWidth="2" strokeLinecap="round" opacity=".8" />
    </svg>
  );
}

/** Crosshair: "use my location". */
export function LocateIcon({ className }: IconProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      className={className}
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="7" />
      <circle cx="12" cy="12" r="2.5" fill="currentColor" stroke="none" />
      <path d="M12 2v3M12 19v3M2 12h3M19 12h3" />
    </svg>
  );
}
