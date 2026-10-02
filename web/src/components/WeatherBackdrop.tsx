/**
 * Animated sky behind a panel's content, one scene per forecast state:
 * clear → clouds gathering → rain soon → drizzle → rain → downpour.
 * Purely decorative; streaks and drifting stop under reduced motion (globals.css).
 */
export type Scene = "clear" | "gathering" | "soon" | "drizzle" | "rain" | "downpour";
export type Glow = "emerald" | "sky" | "rose";

type Rain = {
  count: number;
  /** Seconds per fall, before per-streak jitter. */
  speed: number;
  /** Streak length in px. */
  length: number;
  opacity: number;
  /** Degrees off vertical. */
  slant: number;
};

const RAIN: Partial<Record<Scene, Rain>> = {
  soon: { count: 6, speed: 1.6, length: 36, opacity: 0.3, slant: 0 },
  drizzle: { count: 16, speed: 2.4, length: 14, opacity: 0.45, slant: 0 },
  rain: { count: 16, speed: 1.1, length: 56, opacity: 0.45, slant: 6 },
  downpour: { count: 40, speed: 0.6, length: 90, opacity: 0.6, slant: 14 },
};

/** Cloud count per scene. */
const CLOUDS: Record<Scene, number> = { clear: 0, gathering: 2, soon: 3, drizzle: 2, rain: 2, downpour: 3 };

const GLOW: Record<Glow, string> = {
  emerald: "bg-emerald-400/15",
  sky: "bg-sky-500/15",
  rose: "bg-rose-500/20",
};

/** Deterministic pseudo-random numbers so server and client render alike. */
function rng(seed: number) {
  let s = seed;
  return () => {
    s = (s * 1664525 + 1013904223) % 4294967296;
    return s / 4294967296;
  };
}

export function WeatherBackdrop({ scene, glow }: { scene: Scene; glow: Glow }) {
  const rain = RAIN[scene];
  const r = rng(7);
  const streaks = rain
    ? Array.from({ length: rain.count }, (_, i) => ({
        left: ((i + r()) / rain.count) * 120 - 10, // a little past both edges, for the slant
        delay: r() * 2,
        dur: rain.speed * (0.8 + r() * 0.4),
      }))
    : [];
  const clouds = Array.from({ length: CLOUDS[scene] }, (_, i) => ({
    top: -20 + i * 28 + r() * 20,
    left: -10 + r() * 70,
    width: 140 + r() * 120,
    dur: 16 + r() * 10,
  }));

  return (
    <div aria-hidden="true" className="pointer-events-none absolute -inset-5 overflow-hidden">
      <div className={`absolute -top-16 -right-10 size-48 rounded-full blur-3xl ${GLOW[glow]}`} />
      {scene === "clear" && <div className="sky-sun" />}
      {clouds.map((c, i) => (
        <div
          key={i}
          className={`sky-cloud ${scene === "downpour" ? "bg-slate-400/30" : "bg-slate-400/20"}`}
          style={{ top: c.top, left: `${c.left}%`, width: c.width, animationDuration: `${c.dur}s` }}
        />
      ))}
      {rain && (
        <div className="absolute -inset-10" style={{ transform: `rotate(${rain.slant}deg)` }}>
          {streaks.map((s, i) => (
            <span
              key={i}
              className="rain-streak"
              style={{
                left: `${s.left}%`,
                height: rain.length,
                opacity: rain.opacity,
                animationDelay: `${s.delay}s`,
                animationDuration: `${s.dur}s`,
              }}
            />
          ))}
        </div>
      )}
    </div>
  );
}
