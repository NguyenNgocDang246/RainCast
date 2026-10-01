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
    unselect: "Clear",
    loading: "Checking the radar…",
    footerAddress: "Addresses",
  },

  intro: {
    heading: "How long until rain reaches you?",
    lead: "See the next hour of rain, right where you are.",
    address: "Address",
    addressExample: "Ben Thanh Market, District 1",
    mapsLink: "Google Maps link",
    mapsLinkExample: "Copy the share link",
    coords: "Coordinates",
  },

  search: {
    placeholder: "Address, Google Maps link or coordinates",
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
  },

  admin: {
    tabs: {
      overview: "Overview",
      accuracy: "Accuracy",
      issues: "Forecast history",
      lookups: "Lookups",
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
      stations: (n, names, since) => `${n} stations collecting scoring data: ${names} · running since ${since}`,
      lastError: "Last poll error:",

      stored: "Stored data",
      frames: "Radar frames",
      atStations: (n) => `measured at ${n} stations`,
      issues: "Forecasts (tracked points)",
      forecasts: "Per-lead predictions",
      verified: "Verified",
      lookups: "User lookups",

      geocoding: (on) => `Geocoding (since start) · LocationIQ ${on ? "on" : "off"}`,
      cacheHits: "Served from cache",
      rateLimited: (limited, errors) => `${limited} rate-limited · ${errors} errors`,
      errors: (n) => `${n} errors`,
    },

    accuracy: {
      series: {
        model: "Model",
        persistence: "Persistence (baseline)",
        trend: "Model + trend",
      },
      metrics: {
        csi: "CSI",
        accuracy: "Accuracy",
        pod: "Rain caught",
        far: "False alarms",
      },
      csiModel: "CSI · model",
      csiTrend: "CSI · model + trend",
      csiBaseline: "CSI · baseline",
      accuracyPct: (p) => `accuracy ${p}`,
      noData: "no data yet",
      arrivalError: "Arrival time error",
      noArrivals: "no rain arrivals yet",
      arrivalBias: (n, late, mins) => `${n} cases · rain came ${mins} min ${late ? "later" : "earlier"} than forecast`,
      byLeadShadow: (n) => `By lead time · ${n} predictions with both versions`,
      byLead: (n) => `By lead time · ${n} predictions`,
      nothingYet: "No forecast is old enough to verify yet.",
      chartNote: (n) =>
        `Real data from ${n} station forecasts, scored when the actual radar frame arrives. “Trend” lets rain grow or weaken following the last 20 minutes; it runs alongside for comparison and is not shown to users yet.`,
      table: (n) => `Details · all ${n} verified predictions`,
      lead: "Lead",
      count: "Count",
      podFull: "Rain caught (POD)",
      farFull: "False alarms (FAR)",
      contingency: "Hits / misses / false / correct no-rain",
      maeDbz: "dBZ error",
      tableNote: "Small numbers are the “nothing changes” baseline. CSI = hits / (hits + misses + false alarms), the main score to optimise.",
      metricGroup: "Metric",
      chartLabel: "Score by lead time",
      tooltipCases: (n, c) => `${n} cases · hit/miss/false/correct: ${c}`,
    },

    backtest: {
      title: "Backtest on stored radar data",
      run: "Run backtest",
      running: "Running…",
      never: "Not run yet. Click “Run backtest” to re-score every stored frame.",
      notEnough: (frames) =>
        `Not enough data: needs at least 9 consecutive frames before and 6 after a time (${frames} frames so far).`,
      summary: (issues, from, to, points, frames, at, took) =>
        `${issues} forecast times (${from} → ${to}) × ${points} points around the main station · ${frames} frames · run at ${at} (${took})`,
      config: "Setting",
      csiTotal: "Overall CSI",
      best: "best",
      baseline: "Persistence (baseline)",
      variant: (pairs, trend) => `${pairs} ${pairs === 1 ? "pair" : "pairs"}${trend ? " + trend" : ""}`,
      note: "“N pairs” is how many frame pairs (10 min apart) estimate the motion; “+ trend” adds rain growing or weakening. Every setting is scored on the same times and points. Tiles are kept for 7 days.",
    },

    history: {
      issuesTitle: (n) => `Station forecast history (last ${n})`,
      frame: "Radar frame",
      station: "Station",
      then: "At the time",
      rainIn: "Rain in",
      heavyIn: "Heavy rain in",
      predVsObs: (m) => `+${m}′ pred / obs`,
      issuesNote:
        "Predicted dBZ / dBZ measured when the real frame arrived (… = not yet due). “hit/miss” compares rain (≥ threshold) with no rain.",
      framesTitle: (n) => `Recorded radar frames (${n}) · dBZ at each station`,
      frameTime: "Frame time",
      recordedAt: "Recorded at",
      framesNote: "≥ 20 dBZ is rain, ≥ 40 is heavy rain; “–” means no rain or not measured.",
      lookupsTitle: (n) => `User lookups (last ${n})`,
      at: "At",
      place: "Location",
      result: "Result",
      motion: "Motion",
      viewRadar: "View radar",
      noLookups: "No lookups yet.",
    },

    tools: {
      inspect: "Inspect a location",
      placeholder: "Address, Google Maps link, coordinates — empty = tracked point",
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
