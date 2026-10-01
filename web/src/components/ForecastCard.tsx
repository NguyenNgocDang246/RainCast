import type { Forecast } from "@/lib/api";
import { clock, compass, dropsBelow, peakFrom } from "@/lib/format";
import { useLocale } from "@/lib/i18n";
import type { Dict } from "@/lib/messages/vi";

type Props = { forecast: Forecast; now: number };

type View = { tone: "dry" | "light" | "heavy"; status: string; headline: React.ReactNode; detail?: string };

/**
 * Answers one question: when does it rain — or, if it is already drizzling,
 * when does it pour. Minutes in the API count from the radar frame, which is
 * usually 10–20 minutes old, so everything is shifted to "from now".
 * Very light to light rain (below likely_dbz) is hedged ("possibly"): such
 * echoes often evaporate before reaching the ground.
 */
export function ForecastCard({ forecast: f, now }: Props) {
  const { locale, t } = useLocale();
  const frameMs = new Date(f.frame_time).getTime();
  const fromNow = (minute: number) => Math.max(0, Math.round((frameMs + minute * 60000 - now) / 60000));
  const at = (minute: number) => clock(frameMs + minute * 60000, locale);
  const v = view(f, t, fromNow, at);

  const accent = { dry: "text-emerald-400", light: "text-sky-400", heavy: "text-rose-400" }[v.tone];

  return (
    <section className="w-full max-w-xl text-center">
      <p className={`text-sm font-medium uppercase tracking-widest ${accent}`}>{v.status}</p>
      <h1 className="mt-3 text-4xl font-semibold leading-tight text-slate-50 sm:text-6xl">{v.headline}</h1>
      {v.detail && <p className="mt-4 text-lg text-slate-400">{v.detail}</p>}
      <p className="mt-10 text-xs text-slate-500">
        {t.forecast.radarAt(clock(frameMs, locale))}
        {/* Motion is of the echoes nearby; with no rain coming it only confuses. */}
        {v.tone !== "dry" &&
          f.motion_reliable &&
          f.speed_kmh >= 1 &&
          t.forecast.moving(compass(f.direction_deg, locale), f.speed_kmh.toFixed(0))}
      </p>
    </section>
  );
}

function view(f: Forecast, t: Dict, fromNow: (m: number) => number, at: (m: number) => string): View {
  const s = t.forecast;
  const mins = (m: number) => <Minutes text={t.units.min(fromNow(m))} />;

  if (f.heavy_now) {
    const ease = dropsBelow(f, f.heavy_dbz);
    return {
      tone: "heavy",
      status: s.heavyNow,
      // The radar frame is 10–20 min old, so the easing may already be due.
      headline:
        ease < 0 ? s.heavyContinues : fromNow(ease) === 0 ? s.easingNow : <>{s.easingIn} {mins(ease)}</>,
      detail: ease < 0 ? s.noEasing : s.about(at(ease)),
    };
  }
  if (f.raining_now) {
    const status = (f.series[0]?.dbz ?? -32) < f.likely_dbz ? s.maybeLightNow : s.lightNow;
    if (f.heavy_arrival_min >= 0) {
      return {
        tone: "light",
        status,
        headline:
          fromNow(f.heavy_arrival_min) === 0 ? s.heavySoon : <>{s.heavyIn} {mins(f.heavy_arrival_min)}</>,
        detail: s.about(at(f.heavy_arrival_min)),
      };
    }
    const stop = dropsBelow(f, f.threshold_dbz);
    return {
      tone: "light",
      status,
      headline: s.noHeavy,
      detail: stop < 0 ? s.lightLasts : s.mayStop(at(stop)),
    };
  }
  if (f.arrival_min >= 0) {
    const heavy = f.heavy_arrival_min;
    // Judge the incoming rain by its strongest part, not its leading edge.
    if (heavy < 0 && peakFrom(f, f.arrival_min) < f.likely_dbz) {
      return {
        tone: "light",
        status: s.maybeSoon,
        headline: fromNow(f.arrival_min) === 0 ? s.maybeSoon : <>{s.maybeIn} {mins(f.arrival_min)}</>,
        detail: s.drizzle(at(f.arrival_min)),
      };
    }
    return {
      tone: "light",
      status: s.soon,
      headline: fromNow(f.arrival_min) === 0 ? s.soon : <>{s.rainIn} {mins(f.arrival_min)}</>,
      detail: heavy >= 0 ? s.heavyAfter(fromNow(heavy), at(heavy)) : s.moderate(at(f.arrival_min)),
    };
  }
  return { tone: "dry", status: s.dry, headline: s.noRain };
}

function Minutes({ text }: { text: string }) {
  return <span className="whitespace-nowrap tabular-nums text-sky-400">{text}</span>;
}
