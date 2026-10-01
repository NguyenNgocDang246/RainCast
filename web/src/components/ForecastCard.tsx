import type { Forecast } from "@/lib/api";
import { clock, compass, dropsBelow, peakFrom } from "@/lib/format";

type Props = { forecast: Forecast; now: number };

type View = { tone: "dry" | "light" | "heavy"; status: string; headline: React.ReactNode; detail?: string };

/**
 * Answers one question: when does it rain — or, if it is already drizzling,
 * when does it pour. Minutes in the API count from the radar frame, which is
 * usually 10–20 minutes old, so everything is shifted to "from now".
 * Very light to light rain (below likely_dbz) is hedged with "Có thể": such
 * echoes often evaporate before reaching the ground.
 */
export function ForecastCard({ forecast: f, now }: Props) {
  const frameMs = new Date(f.frame_time).getTime();
  const fromNow = (minute: number) => Math.max(0, Math.round((frameMs + minute * 60000 - now) / 60000));
  const at = (minute: number) => clock(frameMs + minute * 60000);
  const v = view(f, fromNow, at);

  const accent = { dry: "text-emerald-400", light: "text-sky-400", heavy: "text-rose-400" }[v.tone];

  return (
    <section className="w-full max-w-xl text-center">
      <p className={`text-sm font-medium uppercase tracking-widest ${accent}`}>{v.status}</p>
      <h1 className="mt-3 text-4xl font-semibold leading-tight text-slate-50 sm:text-6xl">{v.headline}</h1>
      {v.detail && <p className="mt-4 text-lg text-slate-400">{v.detail}</p>}
      <p className="mt-10 text-xs text-slate-500">
        Radar lúc {clock(frameMs)}
        {f.motion_reliable && f.speed_kmh >= 1 && ` · mưa đang di chuyển về hướng ${compass(f.direction_deg)}, ${f.speed_kmh.toFixed(0)} km/h`}
      </p>
    </section>
  );
}

function view(f: Forecast, fromNow: (m: number) => number, at: (m: number) => string): View {
  const mins = (m: number) => <Minutes n={fromNow(m)} />;

  if (f.heavy_now) {
    const ease = dropsBelow(f, f.heavy_dbz);
    return {
      tone: "heavy",
      status: "Đang mưa to",
      // The radar frame is 10–20 min old, so the easing may already be due.
      headline:
        ease < 0 ? <>Mưa to còn kéo dài</> : fromNow(ease) === 0 ? <>Đang dịu bớt</> : <>Dịu bớt sau {mins(ease)}</>,
      detail: ease < 0 ? "Chưa thấy dấu hiệu ngớt trong 60 phút tới" : `Khoảng ${at(ease)}`,
    };
  }
  if (f.raining_now) {
    const status = (f.series[0]?.dbz ?? -32) < f.likely_dbz ? "Có thể đang mưa nhẹ" : "Đang mưa nhẹ";
    if (f.heavy_arrival_min >= 0) {
      return {
        tone: "light",
        status,
        headline:
          fromNow(f.heavy_arrival_min) === 0 ? <>Sắp mưa to</> : <>Mưa to sau {mins(f.heavy_arrival_min)}</>,
        detail: `Khoảng ${at(f.heavy_arrival_min)}`,
      };
    }
    const stop = dropsBelow(f, f.threshold_dbz);
    return {
      tone: "light",
      status,
      headline: <>Chưa có mưa to</>,
      detail: stop < 0 ? "Mưa nhẹ có thể kéo dài hơn 60 phút" : `Có thể tạnh khoảng ${at(stop)}`,
    };
  }
  if (f.arrival_min >= 0) {
    const heavy = f.heavy_arrival_min;
    // Judge the incoming rain by its strongest part, not its leading edge.
    if (heavy < 0 && peakFrom(f, f.arrival_min) < f.likely_dbz) {
      return {
        tone: "light",
        status: "Có thể sắp mưa",
        headline:
          fromNow(f.arrival_min) === 0 ? <>Có thể sắp mưa</> : <>Có thể mưa sau {mins(f.arrival_min)}</>,
        detail: `Khoảng ${at(f.arrival_min)}, mưa rất nhẹ hoặc lất phất`,
      };
    }
    return {
      tone: "light",
      status: "Sắp mưa",
      headline: fromNow(f.arrival_min) === 0 ? <>Sắp mưa</> : <>Mưa sau {mins(f.arrival_min)}</>,
      detail: heavy >= 0 ? `Mưa to sau khoảng ${fromNow(heavy)} phút (${at(heavy)})` : `Khoảng ${at(f.arrival_min)}, mưa vừa`,
    };
  }
  return { tone: "dry", status: "Trời tạm ổn", headline: <>Không mưa trong 60 phút tới</> };
}

function Minutes({ n }: { n: number }) {
  return (
    <span className="whitespace-nowrap tabular-nums text-sky-400">
      {n} phút
    </span>
  );
}
