import { useLocale } from "@/lib/i18n";
import { LocateIcon, Logo } from "./icons";
import { WeatherBackdrop } from "./WeatherBackdrop";

type Props = {
  /** Asks the browser for the user's position. */
  onLocate: () => void;
  locating: boolean;
};

/** Shown until the user picks a place. */
export function Intro({ onLocate, locating }: Props) {
  const { t } = useLocale();
  const [before, accent, after] = t.intro.heading;
  return (
    <section className="relative w-full">
      <WeatherBackdrop scene="rain" glow="sky" />
      <div className="relative">
        <div className="flex items-center gap-2">
          <Logo className="size-7" />
          <span className="text-lg font-semibold tracking-tight text-slate-50">Raincast</span>
        </div>
        <h1 className="mt-5 text-2xl font-semibold leading-tight text-slate-50 sm:text-3xl">
          {before}
          <span className="bg-linear-to-r from-sky-300 to-cyan-200 bg-clip-text text-transparent">{accent}</span>
          {after}
        </h1>
        <p className="mt-3 text-slate-400">{t.intro.lead}</p>
        <button
          type="button"
          onClick={onLocate}
          disabled={locating}
          className="mt-6 flex w-full cursor-pointer items-center justify-center gap-2 rounded-xl bg-sky-500 px-4 py-3 font-medium text-slate-950 transition hover:bg-sky-400 disabled:cursor-wait disabled:bg-sky-500/60"
        >
          <LocateIcon className={`size-5 ${locating ? "animate-spin" : ""}`} />
          {locating ? t.locate.busy : t.locate.cta}
        </button>
      </div>
    </section>
  );
}
