import type { Dict } from "./vi";

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
    "could not load radar data for this location": "Couldn't load radar for this location.",
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
    moving: (dir, kmh) => ` · rain moving ${dir}, ${kmh} km/h`,
    about: (clock) => `Around ${clock}`,

    heavyNow: "Heavy rain now",
    heavyContinues: "Heavy rain continues",
    easingNow: "Easing off now",
    easingIn: "Easing in",
    noEasing: "No sign of letting up in the next 60 minutes",

    maybeLightNow: "Possibly light rain now",
    lightNow: "Light rain now",
    heavySoon: "Heavy rain soon",
    heavyIn: "Heavy rain in",
    noHeavy: "No heavy rain yet",
    lightLasts: "Light rain may last over 60 minutes",
    mayStop: (clock) => `May stop around ${clock}`,

    maybeSoon: "Rain possible soon",
    maybeIn: "Rain possible in",
    drizzle: (clock) => `Around ${clock}, very light rain or drizzle`,
    soon: "Rain soon",
    rainIn: "Rain in",
    heavyAfter: (n, clock) => `Heavy rain in about ${n} min (${clock})`,
    moderate: (clock) => `Around ${clock}, moderate rain`,

    dry: "Looking dry",
    noRain: "No rain in the next 60 minutes",
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
      group: "Group",
      regionsCol: "Regions",
      eventsCol: "Rain events",
      baselineCsi: "Persistence CSI",
      climates: { tropical: "tropical", subtropical: "subtropical", midlat: "mid-latitude" },
      bestCol: "Best setting",
      config: "Setting",
      csiTotal: "Overall CSI",
      best: "best",
      baseline: "Persistence (baseline)",
      variant: (pairs, trend) => `${pairs} ${pairs === 1 ? "pair" : "pairs"}${trend ? " + trend" : ""}`,
      methods: {
        trec: "TREC",
        cotrec: "COTREC",
        hs: "Horn–Schunck",
        lk: "Lucas–Kanade",
        "cell-nn": "Cell NN",
        "cell-hung": "Cell Hungarian",
        hybrid: "Hybrid",
        ensemble: "Ensemble mean",
        vote: "Ensemble vote",
      },
      pairsSuffix: (pairs) => ` ${pairs} ${pairs === 1 ? "pair" : "pairs"}`,
      trendSuffix: " + trend",
      csiCi: "Overall CSI [95%]",
      delta: "Δ vs TREC 4 pairs",
      bss: "BSS",
      auc: "AUC",
      ms: "ms/run",
      better: "surely better",
      worse: "surely worse",
      intervals: (blocks) => `95% intervals from ${blocks} six-hour blocks.`,
      note: "Δ: CSI difference from TREC 4 pairs (what the app serves); ▲/▼ when the whole interval is above/below 0, so the difference is not chance. BSS: probability skill over persistence (0 = no better, closer to 1 is better). AUC: how well rain is told from no rain (0.5 = guessing, 1 = perfect). ms/run: rough compute time. “+ trend” adds rain growing or weakening. Every setting is scored on the same times and points (only where radar covers); scores add up across runs.",
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
      threshold: "Thresholds",
      thresholdValue: (rain, heavy) => `rain ≥ ${rain} dBZ · heavy ≥ ${heavy} dBZ`,
      minuteFromFrame: "Minute (from frame)",
      predictedDbz: "Predicted dBZ",
    },
  },
};
