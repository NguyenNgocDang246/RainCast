import type { Forecast } from "@/lib/api";
import { clock, compass, dropsBelow, peakFrom } from "@/lib/format";
import { forecastAt, type ForecastNow } from "@/lib/forecastNow";
import { useLocale } from "@/lib/i18n";
import type { Dict } from "@/lib/messages/vi";
import { WeatherBackdrop, type Glow, type Scene } from "./WeatherBackdrop";

type Props = { forecast: Forecast; now: number };

type View = {
  tone: "dry" | "light" | "heavy";
  /** The backdrop: the weather now, or what is coming when it is dry. */
  scene: Scene;
  /** Heavy rain coming tints a lighter scene rose. */
  heavySoon?: boolean;
  /** The weather now. */
  status: string;
  /** What comes next. */
  headline: React.ReactNode;
  /** When, and how much. */
  detail?: string;
};

/**
 * Says the weather now (status), then what comes next (headline): when it
 * rains, pours, eases or stops. Minutes in the API count from the radar frame, which is
 * usually 10–20 minutes old, so the forecast is read as of `now` (forecastAt)
 * and everything is shifted to "from now".
 * Very light to light rain (below likely_dbz) is hedged ("possibly"): such
 * echoes often evaporate before reaching the ground.
 */
export function ForecastCard({ forecast, now }: Props) {
  const { locale, t } = useLocale();
  const f = forecastAt(forecast, now);
  const frameMs = new Date(f.frame_time).getTime();
  const fromNow = (minute: number) => Math.max(0, Math.round((frameMs + minute * 60000 - now) / 60000));
  const at = (minute: number) => clock(frameMs + minute * 60000, locale);
  const v = view(f, t, fromNow, at);
  const rate = hourlyRate(f);

  const accent = { dry: "text-emerald-400", light: "text-sky-400", heavy: "text-rose-400" }[v.tone];
  const glow: Glow = v.tone === "heavy" || v.heavySoon ? "rose" : v.tone === "dry" ? "emerald" : "sky";

  return (
    <section className="relative w-full">
      <WeatherBackdrop scene={v.scene} glow={glow} />
      <div className="relative">
        <p className={`text-sm font-medium uppercase tracking-widest ${accent}`}>{v.status}</p>
        <h1 className="mt-3 text-3xl font-semibold leading-tight text-slate-50 sm:text-4xl">{v.headline}</h1>
        {v.detail && <p className="mt-3 text-slate-400">{v.detail}</p>}
        {v.tone !== "dry" && rate >= 0.1 && (
          <p className="mt-2 text-sm text-slate-400">{t.forecast.accum(mm(rate, locale))}</p>
        )}
        {(f.storms_nearby ?? 0) > 0 && (
          <p className="mt-2 text-sm text-amber-300/90">{t.forecast.storms(f.storms_nearby ?? 0)}</p>
        )}
        <p className="mt-6 text-xs text-slate-500">
          {t.forecast.radarAt(clock(frameMs, locale))}
          {/* Motion is of the echoes nearby; with no rain coming it only confuses. */}
          {v.tone !== "dry" &&
            f.motion_reliable &&
            (isStill(f) ? t.forecast.stationary : t.forecast.moving(compass(f.direction_deg, locale), f.speed_kmh.toFixed(0)))}
        </p>
      </div>
    </section>
  );
}

/** Closer than this, a count of minutes is false precision: it is just "soon". */
const SOON_MIN = 3;

function view(f: ForecastNow, t: Dict, fromNow: (m: number) => number, at: (m: number) => string): View {
  const s = t.forecast;
  /** "<prefix> N min", or `soon` when it is under SOON_MIN minutes away. */
  const inMins = (m: number, soon: string, prefix: string) =>
    fromNow(m) < SOON_MIN ? (
      soon
    ) : (
      <>
        {prefix} <Minutes text={t.units.min(fromNow(m))} />
      </>
    );
  /** Minutes the forecast still reaches: its hour started at an older frame. */
  const left = fromNow(f.series.at(-1)?.minute ?? 0);

  if (f.heavy_now) {
    const ease = dropsBelow(f, f.heavy_dbz, f.m0);
    return {
      tone: "heavy",
      scene: "downpour",
      status: s.heavyNow,
      headline: ease < 0 ? s.heavyContinues : inMins(ease, s.easingSoon, s.easingIn),
      detail: ease < 0 ? s.noEasing(left) : s.about(at(ease)),
    };
  }
  if (f.raining_now) {
    const maybe = (f.series.find((p) => p.minute === f.m0)?.dbz ?? -32) < f.likely_dbz;
    const status = maybe ? s.maybeLightNow : s.rainNow;
    const scene: Scene = maybe ? "drizzle" : "rain";
    if (f.heavy_arrival_min >= 0) {
      return {
        tone: "light",
        scene,
        heavySoon: true,
        status,
        headline: inMins(f.heavy_arrival_min, s.heavySoon, s.heavyIn),
        detail: s.about(at(f.heavy_arrival_min)),
      };
    }
    const stop = dropsBelow(f, f.threshold_dbz, f.m0);
    return {
      tone: "light",
      scene,
      status,
      headline: stop < 0 ? s.rainLasts : inMins(stop, s.stopSoon, s.stopIn),
      detail: stop < 0 ? s.noStop(left) : s.about(at(stop)),
    };
  }
  if (f.arrival_min >= 0) {
    const heavy = f.heavy_arrival_min;
    // Judge the incoming rain by its strongest part, not its leading edge.
    if (heavy < 0 && peakFrom(f, f.arrival_min) < f.likely_dbz) {
      return {
        tone: "light",
        scene: "gathering",
        status: s.dryNow,
        headline: inMins(f.arrival_min, s.maybeSoon, s.maybeIn),
        detail: s.drizzle(at(f.arrival_min)),
      };
    }
    return {
      tone: "light",
      scene: "soon",
      heavySoon: heavy >= 0,
      status: s.dryNow,
      headline: inMins(f.arrival_min, s.soon, s.rainIn),
      detail: heavy >= 0 ? s.heavyAfter(fromNow(heavy), at(heavy)) : s.moderate(at(f.arrival_min)),
    };
  }
  return { tone: "dry", scene: "clear", status: s.dryNow, headline: s.noRain(left), detail: s.noRainDetail };
}

/** Below this the motion is under a radar pixel per 10-minute frame: its direction is noise. */
const STILL_KMH = 5;
/** Below this, moving the rain explains the last radar change hardly better than standing still. */
const STILL_GAIN = 0.2;

/** The rain is growing or fading in place rather than travelling, so a heading would mislead. */
function isStill(f: Forecast) {
  return f.speed_kmh < STILL_KMH || (f.motion_gain !== undefined && f.motion_gain < STILL_GAIN);
}

/**
 * The rain still expected, as mm per hour: from now on the series covers
 * less than the hour it was made for, so its total is spread over what is left.
 */
function hourlyRate(f: ForecastNow) {
  const left = (f.series.at(-1)?.minute ?? 0) - f.m0;
  return left > 0 ? ((f.accum_mm ?? 0) * 60) / left : 0;
}

/** Rain totals to a sensible precision: radar estimates are rough. */
function mm(v: number, locale: string) {
  return v.toLocaleString(locale, { maximumFractionDigits: v < 10 ? 1 : 0 });
}

function Minutes({ text }: { text: string }) {
  return <span className="whitespace-nowrap tabular-nums text-sky-400">{text}</span>;
}
