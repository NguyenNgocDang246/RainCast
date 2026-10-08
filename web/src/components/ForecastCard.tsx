import type { Forecast } from "@/lib/api";
import { clock, compass, dropsBelow, peakFrom, risesTo } from "@/lib/format";
import { forecastAt, type ForecastNow } from "@/lib/forecastNow";
import { useLocale } from "@/lib/i18n";
import type { Dict, Span } from "@/lib/messages/vi";
import { WeatherBackdrop, type Glow, type Scene } from "./WeatherBackdrop";

type Props = { forecast: Forecast; now: number };

type View = {
  /** The weather now, which colours the status. */
  now: "dry" | "rain" | "heavy";
  /** The weather now and coming, which colours the glow. */
  tone: "dry" | "light" | "heavy";
  /** The backdrop: the weather now, or what is coming when it is dry. */
  scene: Scene;
  /** Heavy rain coming tints a lighter scene rose. */
  heavySoon?: boolean;
  /** The weather now. */
  status: string;
  /** What comes next, with when. */
  headline: React.ReactNode;
  /**
   * The headline's rain: how strong, how long. A later event only when it is
   * what people act on (stop, rain again). Minutes from now first, the clock
   * only as a hint.
   */
  detail?: string;
};

/** The weather the latest radar frame shows (the API's own summary, from minute 0). */
type Observed = { raining: boolean; heavy: boolean };

/**
 * Says the weather now (status), then what comes next (headline): when it
 * rains, pours, eases or stops, then how strong and how long (detail). Minutes in the API
 * count from the radar frame, which is usually 10–20 minutes old, so the
 * forecast is read as of `now` (forecastAt) and everything is shifted to
 * "from now". A status the latest frame does not show yet is hedged
 * ("possibly"), as is very light to light rain (below likely_dbz): such
 * echoes often evaporate before reaching the ground.
 */
export function ForecastCard({ forecast, now }: Props) {
  const { locale, t } = useLocale();
  const f = forecastAt(forecast, now);
  const frameMs = new Date(f.frame_time).getTime();
  const fromNow = (minute: number) => Math.max(0, Math.round((frameMs + minute * 60000 - now) / 60000));
  const at = (minute: number) => clock(frameMs + minute * 60000, locale);
  const v = view(f, { raining: forecast.raining_now, heavy: forecast.heavy_now }, t, fromNow, at);
  const rate = hourlyRate(f);

  const accent = { dry: "text-emerald-400", rain: "text-sky-400", heavy: "text-rose-400" }[v.now];
  const glow: Glow = v.tone === "heavy" || v.heavySoon ? "rose" : v.tone === "dry" ? "emerald" : "sky";

  return (
    <section className="relative w-full">
      <WeatherBackdrop scene={v.scene} glow={glow} />
      <div className="relative">
        <p className={`text-sm font-medium uppercase tracking-widest ${accent}`}>{v.status}</p>
        <h1 className="mt-3 text-balance text-2xl font-semibold leading-tight text-slate-50 sm:text-3xl">
          {v.headline}
        </h1>
        {v.detail && <p className="mt-3 text-pretty text-sm text-slate-400 sm:text-base">{v.detail}</p>}
        {v.tone !== "dry" && rate >= 0.1 && (
          <p className="mt-2 text-pretty text-xs text-slate-400 sm:text-sm">{t.forecast.accum(mm(rate, locale))}</p>
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
/** Rain shorter than this is a passing shower: its stop time is not worth giving. */
const BRIEF_MIN = 5;

function view(
  f: ForecastNow,
  obs: Observed,
  t: Dict,
  fromNow: (m: number) => number,
  at: (m: number) => string,
): View {
  const s = t.forecast;
  /** "<prefix> N min (HH:MM)", or `soon` when it is under SOON_MIN minutes away. */
  const inMins = (m: number, soon: string, prefix: string) =>
    fromNow(m) < SOON_MIN ? (
      soon
    ) : (
      <>
        {prefix}{" "}
        <span className="whitespace-nowrap">
          <Minutes text={t.units.min(fromNow(m))} /> {/* Phones keep the card short: the minutes say enough there. */}
          <span className="hidden text-slate-400 sm:inline">({at(m)})</span>
        </span>
      </>
    );
  const last = f.series.at(-1)?.minute ?? 0;
  /** Minutes the forecast still reaches: its hour started at an older frame. */
  const left = fromNow(last);
  /**
   * How long the rain (at least `dbz`) arriving at `from` lasts: until it
   * stops, or at least until the forecast's end.
   */
  const until = (from: number, dbz = f.threshold_dbz): [number, string, Span] => {
    const stop = dropsBelow(f, dbz, from);
    // Rain starting just before the forecast ends: how long it lasts is unknown.
    if (stop < 0) return [last - from, at(last), last - from < BRIEF_MIN ? "open" : "past"];
    // A stop time a minute or two after the start reads as a glitch.
    return [stop - from, at(stop), stop - from < BRIEF_MIN ? "brief" : "until"];
  };

  if (f.heavy_now) {
    const ease = dropsBelow(f, f.heavy_dbz, f.m0);
    // Never before it eases (the threshold is lower); the same minute when it stops outright.
    const stop = dropsBelow(f, f.threshold_dbz, f.m0);
    return {
      now: "heavy",
      tone: "heavy",
      scene: "downpour",
      status: obs.heavy ? s.heavyNow : s.maybeHeavyNow,
      headline: ease < 0 ? s.heavyContinues : inMins(ease, s.easingSoon, s.easingIn),
      detail:
        ease < 0
          ? s.noEasing(left)
          : stop < 0
            ? s.noStop(left)
            : stop === ease
              ? s.stopWith
              : s.stopAt(fromNow(stop), at(stop)),
    };
  }
  if (f.raining_now) {
    const maybe = (f.series.find((p) => p.minute === f.m0)?.dbz ?? -32) < f.likely_dbz;
    const status = obs.heavy ? s.maybeEased : maybe ? s.maybeLightNow : obs.raining ? s.rainNow : s.maybeRainNow;
    const scene: Scene = maybe ? "drizzle" : "rain";
    const heavy = f.heavy_arrival_min;
    if (heavy >= 0) {
      const [n, clockAt, span] = until(heavy, f.heavy_dbz);
      return {
        now: "rain",
        tone: "light",
        scene,
        heavySoon: true,
        status,
        headline: inMins(heavy, s.heavySoon, s.heavyIn),
        detail: span === "open" ? undefined : s.heavyFor(n, clockAt, span),
      };
    }
    const stop = dropsBelow(f, f.threshold_dbz, f.m0);
    const again = stop < 0 ? -1 : risesTo(f, f.threshold_dbz, stop);
    return {
      now: "rain",
      tone: "light",
      scene,
      status,
      headline: stop < 0 ? s.rainLasts : inMins(stop, s.stopSoon, s.stopIn),
      detail:
        stop < 0
          ? s.noStop(left)
          : again >= 0
            ? s.againAt(again - stop, at(again))
            : // Dry only for the forecast's last few minutes says nothing.
              last - stop < BRIEF_MIN
              ? undefined
              : s.dryAfter(last - stop),
    };
  }
  const status = obs.raining ? s.maybeStopped : s.dryNow;
  if (f.arrival_min >= 0) {
    const heavy = f.heavy_arrival_min;
    // Judge the incoming rain by its strongest part, not its leading edge.
    if (heavy < 0 && peakFrom(f, f.arrival_min) < f.likely_dbz) {
      return {
        now: "dry",
        tone: "light",
        scene: "gathering",
        status,
        headline: inMins(f.arrival_min, s.maybeSoon, s.maybeIn),
        detail: s.drizzle(...until(f.arrival_min)),
      };
    }
    return {
      now: "dry",
      tone: "light",
      scene: "soon",
      heavySoon: heavy >= 0,
      status,
      headline: inMins(f.arrival_min, s.soon, s.rainIn),
      detail:
        heavy < 0
          ? s.moderate(...until(f.arrival_min))
          : // "In N minutes" right after the headline's own would read as a second, separate rain.
            heavy - f.arrival_min < BRIEF_MIN
            ? s.heavyAtOnce
            : s.heavyAfter(fromNow(heavy), at(heavy)),
    };
  }
  return { now: "dry", tone: "dry", scene: "clear", status, headline: s.noRain(left), detail: s.noRainDetail };
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
