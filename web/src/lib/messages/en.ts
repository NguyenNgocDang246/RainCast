import type { Dict, Span } from "./vi";

function lasting(text: string, n: number, clock: string, span: Span) {
  if (span === "open") return text;
  return `${text}, ${{
    brief: "only briefly",
    until: `lasting about ${n} min (until ${clock})`,
    past: `lasting at least ${n} min`,
  }[span]}`;
}

export const en: Dict = {
  meta: {
    title: "Raincast – How long until it rains?",
    adminTitle: "Raincast – Admin",
  },
  language: { label: "Language" },

  units: {
    min: (n) => `${n} min`,
    minShort: (n) => `${n} min`,
    secondsAgo: (n) => `${n} s ago`,
    minutesAgo: (n) => `${n} min ago`,
    hoursAgo: (n) => `${n} h ago`,
  },

  compass: ["north", "northeast", "east", "southeast", "south", "southwest", "west", "northwest"],

  errors: {
    "link has no location": "This link has no location. Open the place in Google Maps and share it again.",
    "address lookup failed": "Couldn't look up the address. Try again in a few seconds.",
    "radar data is still loading; try again shortly": "Radar data is loading. Try again in a few seconds.",
    "could not load radar data for this location": "Couldn't load radar for this location. Try again in a few minutes.",
    "q is too long (max 500 characters)": "That search is too long. Try a shorter one.",
    "network error": "Couldn't reach the server. Check your connection and try again.",
  },

  problem: {
    busy: "The server is busy. Try again in a few seconds.",
    generic: "Something went wrong. Try again in a few minutes.",
  },

  dashboard: {
    loading: "Checking the radar…",
    footerAddress: "Addresses & map",
  },

  intro: {
    heading: ["How long until ", "rain", " reaches you?"],
    lead: "See the next hour of rain, right where you are.",
  },

  map: {
    label: "Map for picking a place",
    legend: "Rain",
    levels: { veryHeavy: "Very heavy", heavy: "Heavy", moderate: "Moderate", light: "Light" },
  },

  locate: {
    cta: "Use my location",
    button: "My location",
    busy: "Finding you…",
    denied: "Location access is blocked. Allow it in your browser settings, or search by address.",
    failed: "Couldn't get your location. Try again or search by address.",
    insecure: "Browsers only share your location with https pages. Search by address instead.",
  },

  search: {
    placeholder: "Search",
    label: "Find a place",
    submit: "Search",
    busy: "Searching…",
    recent: "Recent searches",
    clear: "Clear",
    notFound: (q) => `Nothing found for “${q}”. Try pasting the place's Google Maps link.`,
  },

  forecast: {
    radarAt: (clock) => `Radar at ${clock}`,
    accum: (mm) => `About ${mm} mm/h of rain expected`,
    moving: (dir, kmh) => ` · rain moving ${dir}, ${kmh} km/h`,
    stationary: " · rain nearly stationary",

    heavyNow: "Heavy rain now",
    rainNow: "Raining now",
    maybeLightNow: "Possibly light rain now",
    dryNow: "Not raining",
    maybeHeavyNow: "Possibly heavy rain now",
    maybeRainNow: "Possibly raining now",
    maybeEased: "Possibly easing off",
    maybeStopped: "Possibly stopped",

    heavyContinues: "Heavy rain continues",
    easingSoon: "Easing off soon",
    easingIn: "Easing in",
    heavySoon: "Heavy rain soon",
    heavyIn: "Heavy rain in",
    rainLasts: "Rain continues",
    stopSoon: "Stopping soon",
    stopIn: "Stopping in",
    soon: "Rain soon",
    rainIn: "Rain in",
    maybeSoon: "Light rain soon",
    maybeIn: "Light rain in",
    noRain: (n) => `No rain in the next ${n} minutes`,

    noEasing: (n) => `No sign of letting up in the next ${n} minutes`,
    noStop: (n) => `No sign of stopping in the next ${n} minutes`,
    stopAt: (n, clock) => `May stop completely in about ${n} min (${clock})`,
    stopWith: "May stop completely",
    againAt: (n, clock) => `Dry for only about ${n} min, then may rain again (${clock})`,
    dryAfter: (n) => `Then no more rain for at least ${n} min`,
    drizzle: (n, clock, span) => lasting("Very light rain", n, clock, span),
    heavyFor: (n, clock, span) =>
      ({
        brief: "Heavy only briefly",
        until: `Heavy rain may last about ${n} min (until ${clock})`,
        past: `Heavy rain may last at least ${n} min`,
      })[span],
    heavyAfter: (n, clock) => `May turn heavy in about ${n} min (${clock})`,
    heavyAtOnce: "May be heavy from the start",
    moderate: (n, clock, span) => lasting("Moderate rain", n, clock, span),
    noRainDetail: "Radar shows no rain heading your way",
  },

  admin: {
    tabs: {
      overview: "Overview",
      backtest: "Backtest",
      frames: "Radar frames",
      tools: "Tools",
    },

    ui: {
      error: (msg) => `Error: ${msg}`,
      loading: "Loading…",
      hit: "hit",
      miss: "miss",
    },

    state: {
      heavy: "heavy rain",
      raining: "raining",
      heavyNow: "heavy rain now",
      dry: "dry",
    },

    overview: {
      pipeline: "Radar pipeline",
      latestFrame: "Latest radar frame",
      lastPoll: "Last poll",
      ticks: (n) => `${n} since start`,
      framesInFeed: "Frames in feed",
      feedKeeps: "RainViewer keeps ~2 hours",
      cachedRegions: "Cached regions",
      forOnDemand: "for on-demand forecasts",
      stations: (n, names, since) => `Default location: ${names.split(", ")[0]} · ${n} preset places · running since ${since}`,
      lastError: "Last poll error:",

      stored: "Collected data (cmd/collect)",
      frames: "Radar frames",
      regions: "Regions tracked",
      noDatabase: "No database connected (DATABASE_URL)",

      geocoding: (on) => `Geoapify (since start) · ${on ? "key set" : "no key"}`,
      cacheHits: "Served from cache",
      failed: "Failed",
      rateLimited: (n) => `${n} rate-limited`,
      tiles: "Map tiles fetched",
      tilesSub: (cached, errors) => `${cached} from cache · ${errors} errors`,

    },

    backtest: {
      title: "Backtest on stored radar data",
      howToRun: "refresh: go run ./cmd/backtest",
      never: "No results yet. Run go run ./cmd/backtest once go run ./cmd/collect has gathered data.",
      notEnough: (frames) =>
        `Not enough data: needs at least 9 consecutive frames before and 6 after a time (${frames} frames so far).`,
      summary: (issues, from, to, points, frames, at, took) =>
        `${issues} forecast times (${from} → ${to}) × up to ${points} points per region · ${frames} frames · run at ${at} (${took})`,
      events: (events, regions) => `${events} rain events from ${regions} regions.`,
      fewEvents: (need) => `Fewer than ${need} rain events: differences between settings may still be noise.`,
      groups: "By climate group",
      classes: "By kind of rain",
      classesNote:
        "Overall CSI on each kind of rain and Δ from TREC 4 pairs. For a band (light, moderate, heavy) a hit needs the right band; for a threshold (… or worse) reaching it is enough. Scored point by point, so rain a little off is a miss. Kinds with few observed samples have noisy scores.",
      classLabel: (lo, hi) => en.admin.backtest.classNames[`${lo}-${hi ?? ""}`],
      classNames: {
        "20-30": "Light",
        "30-40": "Moderate",
        "40-50": "Heavy",
        "50-": "Very heavy",
        "30-": "Moderate or worse",
        "40-": "Heavy or worse",
      },
      fss: "Allowing for position errors (FSS)",
      fssNote:
        "Fractions skill score: compares the share of area raining in a square around each point, so rain forecast a few km off still scores. 1 is perfect, 0 no skill; the small number is the difference from TREC 4 pairs (in points). The ~10 km square is about point by point.",
      fssThreshold: (dbz) => `≥ ${dbz} dBZ`,
      fssWindow: (km) => `~${Math.round(km)} km`,
      observed: (n) => `${n.toLocaleString("en")} samples`,
      group: "Group",
      regionsCol: "Regions",
      eventsCol: "Rain events",
      baselineCsi: "Persistence CSI",
      climates: { tropical: "tropical", subtropical: "subtropical", midlat: "mid-latitude", sat: "seen by satellite", nosat: "no satellite", tropical_sat: "tropical + satellite" },
      bestCol: "Best setting",
      config: "Setting",
      csiTotal: "Overall CSI",
      best: "best",
      baseline: "Persistence (baseline)",
      variant: (pairs, trend) => `${pairs} ${pairs === 1 ? "pair" : "pairs"}${trend ? " + trend" : ""}`,
      methods: {
        trec: "TREC",
        hs: "Horn–Schunck",
        lk: "Lucas–Kanade",
        ensemble: "Ensemble mean",
      },
      pairsSuffix: (pairs) => ` ${pairs} ${pairs === 1 ? "pair" : "pairs"}`,
      trendSuffix: " + trend",
      csiCi: "Overall CSI [95%]",
      delta: "Δ vs TREC 4 pairs",
      bss: "BSS",
      auc: "AUC",
      accum: "mm/h ±",
      ms: "ms/run",
      better: "surely better",
      worse: "surely worse",
      intervals: (blocks) => `95% intervals from ${blocks} six-hour blocks.`,
      note: "Δ: CSI difference from TREC 4 pairs (what the app serves); ▲/▼ when the whole interval is above/below 0, so the difference is not chance. BSS: probability skill over persistence (0 = no better, closer to 1 is better). AUC: how well rain is told from no rain (0.5 = guessing, 1 = perfect). mm/h ±: mean error of the hour's rain total against radar (lower is better). ms/run: rough compute time. “+ trend” adds rain growing or weakening. Every setting is scored on the same times and points (only where radar covers); scores add up across runs.",
    },

    history: {
      framesTitle: (n) => `Recorded radar frames (latest ${n})`,
      frameTime: "Frame time",
      path: "Tile path",
      noFrames: "No frames yet (or no database connected).",
    },

    tools: {
      inspect: "Inspect a location",
      placeholder: "Address, Google Maps link or coordinates",
      pickPlace: "Enter a place to see its radar and forecast.",
      view: "View",
      busy: "Searching…",
      notFound: "Place not found.",
      radarTitle: "Radar image and motion",
      radarAlt: "Radar around the location",
      legend: "Red dot: location · rings 25/50/100 km · arrow: distance rain travels in 60 min · grid: z7 tile edges",
      detail: "Forecast details",
      place: "Location",
      frame: "Radar frame",
      now: "Now",
      rainIn: "Rain in",
      heavyIn: "Heavy rain in",
      motion: "Motion",
      motionValue: (kmh, dir, deg) => `${kmh} km/h heading ${dir} (${deg}°)`,
      noMotion: "not enough data",
      coherence: "Member agreement",
      gain: "Motion explains radar",
      member: (method) => `${method.toUpperCase()} motion`,
      threshold: "Thresholds",
      thresholdValue: (rain, heavy) => `rain ≥ ${rain} dBZ · heavy ≥ ${heavy} dBZ`,
      minuteFromFrame: "Minute (from frame)",
      predictedDbz: "Predicted dBZ",
    },
  },
};
