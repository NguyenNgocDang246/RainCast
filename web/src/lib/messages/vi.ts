// Vietnamese UI text. en.ts must match this shape (see Dict).

/** How long incoming rain lasts (ForecastCard). */
export type Span = "brief" | "until" | "past" | "open";

/** `n`: how long the rain lasts, in minutes; `clock`: when it stops. */
function lasting(text: string, n: number, clock: string, span: Span) {
  if (span === "open") return text;
  return `${text}, ${{
    brief: "chỉ thoáng qua",
    until: `kéo dài khoảng ${n} phút (tới ${clock})`,
    past: `kéo dài ít nhất ${n} phút`,
  }[span]}`;
}

export const vi = {
  meta: {
    title: "Raincast – Khi nào trời mưa?",
    description: "Khi nào trời mưa? Dự báo mưa 60 phút tới ở bất kỳ đâu.",
    adminTitle: "Raincast – Admin",
  },
  language: { label: "Ngôn ngữ" },

  units: {
    min: (n: number) => `${n} phút`,
    minShort: (n: number) => `${n} ph`,
    secondsAgo: (n: number) => `${n} giây trước`,
    minutesAgo: (n: number) => `${n} phút trước`,
    hoursAgo: (n: number) => `${n} giờ trước`,
  },

  compass: ["Bắc", "Đông Bắc", "Đông", "Đông Nam", "Nam", "Tây Nam", "Tây", "Tây Bắc"],

  /** Backend error messages (English, by exact text) shown to people. */
  errors: {
    "link has no location": "Link này không chứa vị trí. Hãy mở địa điểm trong Google Maps rồi chia sẻ lại.",
    "address lookup failed": "Không tra được địa chỉ, thử lại sau ít giây.",
    "radar data is still loading; try again shortly": "Đang tải dữ liệu radar, thử lại sau ít giây.",
    "could not load radar data for this location": "Không tải được radar cho vị trí này, thử lại sau ít phút.",
    "q is too long (max 500 characters)": "Nội dung tìm quá dài, hãy rút gọn lại.",
    "network error": "Không kết nối được máy chủ. Kiểm tra mạng rồi thử lại.",
  } as Record<string, string>,

  /** What visitors see for errors without a message of their own (userError). */
  problem: {
    busy: "Máy chủ đang bận, thử lại sau ít giây.",
    generic: "Có lỗi xảy ra, thử lại sau ít phút.",
  },

  dashboard: {
    loading: "Đang xem radar…",
    footerAddress: "Địa chỉ & bản đồ",
  },

  intro: {
    /** The middle part is highlighted. */
    heading: ["Khi nào ", "trời mưa", "?"],
    lead: "Xem trước mưa trong một giờ tới ở bất kỳ đâu.",
  },

  map: {
    label: "Bản đồ chọn vị trí",
    legend: "Mưa",
    levels: { veryHeavy: "Rất to", heavy: "To", moderate: "Vừa", light: "Nhẹ" },
  },

  locate: {
    cta: "Dùng vị trí của tôi",
    button: "Vị trí của tôi",
    busy: "Đang lấy vị trí…",
    denied: "Trình duyệt đang chặn quyền vị trí. Hãy cho phép trong cài đặt, hoặc tìm theo địa chỉ.",
    failed: "Không lấy được vị trí. Thử lại hoặc tìm theo địa chỉ.",
    insecure: "Trình duyệt chỉ cho lấy vị trí trên trang https. Hãy tìm theo địa chỉ.",
  },

  search: {
    placeholder: "Tìm kiếm",
    label: "Tìm vị trí",
    submit: "Tìm",
    busy: "Đang tìm…",
    recent: "Tìm gần đây",
    clear: "Xoá",
    notFound: (q: string) => `Không tìm thấy “${q}”. Thử dán link Google Maps của địa điểm.`,
  },

  forecast: {
    radarAt: (clock: string) => `Radar lúc ${clock}`,
    accum: (mm: string) => `Lượng mưa ước tính khoảng ${mm} mm/h`,
    moving: (dir: string, kmh: string) => ` · mưa đang di chuyển về hướng ${dir}, ${kmh} km/h`,
    stationary: " · mưa gần như đứng yên",

    // Status: the weather now.
    heavyNow: "Đang mưa to",
    rainNow: "Đang mưa",
    maybeLightNow: "Có thể đang mưa nhẹ",
    dryNow: "Hiện không mưa",
    // Status the forecast gives for now but the latest radar frame does not show yet.
    maybeHeavyNow: "Có thể đang mưa to",
    maybeRainNow: "Có thể đang mưa",
    maybeEased: "Có thể đã dịu bớt",
    maybeStopped: "Có thể đã tạnh",

    // Headline: what comes next.
    heavyContinues: "Mưa to còn kéo dài",
    easingSoon: "Sắp dịu bớt",
    easingIn: "Dịu bớt sau",
    heavySoon: "Sắp mưa to",
    heavyIn: "Mưa to sau",
    rainLasts: "Mưa còn kéo dài",
    stopSoon: "Sắp tạnh",
    stopIn: "Tạnh sau",
    soon: "Sắp mưa",
    rainIn: "Mưa sau",
    // Kept short for one line on a phone: status and detail carry the hedge.
    maybeSoon: "Sắp mưa nhẹ",
    maybeIn: "Mưa nhẹ sau",
    noRain: (n: number) => `Không mưa trong ${n} phút tới`,

    // Detail: the headline's rain, how strong and how long; a later event only
    // when it is what people act on (stop, rain again). A count says what it
    // counts from: "N phút nữa" is from now, "kéo dài N phút" from the
    // headline's moment. The clock is only a hint.
    noEasing: (n: number) => `Chưa thấy dấu hiệu ngớt trong ${n} phút tới`,
    noStop: (n: number) => `Chưa thấy dấu hiệu tạnh trong ${n} phút tới`,
    /** `n`: minutes from now. */
    stopAt: (n: number, clock: string) => `Có thể tạnh hẳn khoảng ${n} phút nữa (${clock})`,
    /** It stops outright the minute it eases. */
    stopWith: "Có thể tạnh hẳn",
    /** `n`: how long the break between the stop and the rain again lasts. */
    againAt: (n: number, clock: string) => `Chỉ tạnh khoảng ${n} phút rồi có thể mưa lại (${clock})`,
    /** `n`: how long it stays dry after the stop, at least (the forecast ends then). */
    dryAfter: (n: number) => `Sau đó chưa thấy mưa lại, ít nhất ${n} phút`,
    /**
     * `n`: how long it lasts, until it stops ("until") or at least until the
     * forecast ends ("past"); `clock`: when it stops. A passing shower
     * ("brief") or rain starting at the forecast's end ("open") gives no time.
     */
    drizzle: (n: number, clock: string, span: Span) => lasting("Mưa rất nhẹ", n, clock, span),
    moderate: (n: number, clock: string, span: Span) => lasting("Mưa vừa", n, clock, span),
    /** How long the heavy rain in the headline lasts; hedged, heavy rain is hard to time. */
    heavyFor: (n: number, clock: string, span: Exclude<Span, "open">) =>
      ({
        brief: "Mưa to chỉ thoáng qua",
        until: `Mưa to có thể kéo dài khoảng ${n} phút (tới ${clock})`,
        past: `Mưa to có thể kéo dài ít nhất ${n} phút`,
      })[span],
    /** `n`: minutes from now; the rain arriving starts lighter. */
    heavyAfter: (n: number, clock: string) => `Có thể có lúc mưa to, khoảng ${n} phút nữa (${clock})`,
    /** The rain arriving is heavy almost from its start. */
    heavyAtOnce: "Có thể mưa to ngay từ đầu",
    noRainDetail: "Radar chưa thấy cơn mưa nào đang tiến về đây",
  },

  admin: {
    tabs: {
      overview: "Tổng quan",
      backtest: "Backtest",
      frames: "Khung radar",
      tools: "Công cụ",
    },

    ui: {
      error: (msg: string) => `Lỗi: ${msg}`,
      loading: "Đang tải…",
      hit: "đúng",
      miss: "sai",
    },

    state: {
      heavy: "mưa to",
      raining: "đang mưa",
      heavyNow: "đang mưa to",
      dry: "khô",
    },

    overview: {
      pipeline: "Pipeline radar",
      latestFrame: "Khung radar mới nhất",
      lastPoll: "Lần poll gần nhất",
      ticks: (n: number) => `${n} lần kể từ khi chạy`,
      framesInFeed: "Khung trong feed",
      feedKeeps: "RainViewer giữ ~2 giờ",
      cachedRegions: "Vùng đang cache",
      forOnDemand: "cho dự báo theo yêu cầu",
      stations: (n: number, names: string, since: string) =>
        `Vị trí mặc định: ${names.split(", ")[0]} · ${n} vị trí đặt sẵn · chạy từ ${since}`,
      lastError: "Lỗi lần poll gần nhất:",

      stored: "Dữ liệu đã thu (cmd/collect)",
      frames: "Khung radar",
      regions: "Vùng theo dõi",
      noDatabase: "Chưa nối database (DATABASE_URL)",

      geocoding: (on: boolean) => `Geoapify (từ lần khởi động) · ${on ? "đã có key" : "chưa có key"}`,
      cacheHits: "Trả từ cache",
      failed: "Lỗi",
      rateLimited: (n: number) => `${n} lần hết lượt`,
      tiles: "Ô bản đồ tải về",
      tilesSub: (cached: number, errors: number) => `${cached} từ cache · ${errors} lỗi`,

    },

    backtest: {
      title: "Backtest trên dữ liệu radar đã lưu",
      howToRun: "cập nhật: go run ./cmd/backtest",
      never: "Chưa có kết quả. Chạy go run ./cmd/backtest sau khi go run ./cmd/collect đã thu được dữ liệu.",
      notEnough: (frames: number) =>
        `Chưa đủ dữ liệu: cần ít nhất 9 khung liên tiếp trước và 6 khung sau một thời điểm (đã có ${frames} khung).`,
      summary: (issues: number, from: string, to: string, points: number, frames: number, at: string, took: string) =>
        `${issues} thời điểm dự báo (${from} → ${to}) × tối đa ${points} điểm mỗi vùng · ${frames} khung · chạy lúc ${at} (${took})`,
      events: (events: number, regions: number) => `${events} đợt mưa từ ${regions} vùng.`,
      fewEvents: (need: number) =>
        `Chưa đủ ${need} đợt mưa: chênh lệch giữa các cấu hình lúc này có thể chỉ là nhiễu.`,
      groups: "Theo nhóm khí hậu",
      classes: "Theo loại mưa",
      classesNote:
        "CSI tổng của từng loại mưa và Δ so với TREC 4 cặp. Với khoảng (nhẹ, vừa, to) phải dự báo đúng khoảng mới tính là trúng; với ngưỡng (từ … trở lên) chỉ cần đạt tới ngưỡng. Chấm từng điểm, lệch vị trí là trượt. Loại có ít mẫu quan sát thì điểm dao động nhiều.",
      classLabel: (lo: number, hi?: number): string | undefined => vi.admin.backtest.classNames[`${lo}-${hi ?? ""}`],
      classNames: {
        "20-30": "Mưa nhẹ",
        "30-40": "Mưa vừa",
        "40-50": "Mưa to",
        "50-": "Mưa rất to",
        "30-": "Từ mưa vừa trở lên",
        "40-": "Từ mưa to trở lên",
      } as Record<string, string>,
      fss: "Cho phép lệch vị trí (FSS)",
      fssNote:
        "Fractions skill score: so tỉ lệ diện tích có mưa trong một ô vuông quanh mỗi điểm, nên dự báo lệch vài km vẫn được điểm. 1 là hoàn hảo, 0 là không có kỹ năng; số nhỏ là chênh lệch so với TREC 4 cặp (điểm %). Ô ~10 km gần như chấm từng điểm.",
      fssThreshold: (dbz: number) => `≥ ${dbz} dBZ`,
      fssWindow: (km: number) => `~${Math.round(km)} km`,
      observed: (n: number) => `${n.toLocaleString("vi")} mẫu`,
      group: "Nhóm",
      regionsCol: "Vùng",
      eventsCol: "Đợt mưa",
      baselineCsi: "CSI giữ nguyên",
      climates: { tropical: "nhiệt đới", subtropical: "cận nhiệt", midlat: "ôn đới", sat: "có vệ tinh", nosat: "không vệ tinh", tropical_sat: "nhiệt đới + có vệ tinh" } as Record<string, string>,
      bestCol: "Cấu hình tốt nhất",
      config: "Cấu hình",
      csiTotal: "CSI tổng",
      best: "tốt nhất",
      baseline: "Giữ nguyên (baseline)",
      variant: (pairs: number, trend: boolean) => `${pairs} cặp${trend ? " + xu hướng" : ""}`,
      methods: {
        trec: "TREC",
        hs: "Horn–Schunck",
        lk: "Lucas–Kanade",
        ensemble: "Ensemble trung bình",
      } as Record<string, string>,
      pairsSuffix: (pairs: number) => ` ${pairs} cặp`,
      trendSuffix: " + xu hướng",
      csiCi: "CSI tổng [95%]",
      delta: "Δ so với TREC 4 cặp",
      bss: "BSS",
      auc: "AUC",
      accum: "mm/giờ ±",
      ms: "ms/lần",
      better: "chắc chắn tốt hơn",
      worse: "chắc chắn kém hơn",
      intervals: (blocks: number) => `Khoảng tin cậy 95% lấy từ ${blocks} khối 6 giờ.`,
      note: "Δ: chênh CSI so với TREC 4 cặp (cấu hình app đang dùng); ▲/▼ khi cả khoảng tin cậy nằm trên/dưới 0, tức khác biệt không phải do ngẫu nhiên. BSS: kỹ năng dự báo xác suất so với giữ nguyên (0 = không hơn, càng gần 1 càng tốt). AUC: khả năng phân biệt có mưa/không mưa (0,5 = đoán bừa, 1 = hoàn hảo). mm/giờ ±: sai số trung bình lượng mưa tích luỹ 1 giờ so với radar (càng thấp càng tốt). ms/lần: thời gian tính ước chừng. “+ xu hướng” thêm mưa mạnh lên/yếu đi. Mọi cấu hình chấm trên cùng thời điểm và điểm (chỉ nơi có radar phủ); điểm được cộng dồn qua các lần chạy.",
    },

    history: {
      framesTitle: (n: number) => `Khung radar đã ghi (${n} gần nhất)`,
      frameTime: "Thời điểm khung",
      path: "Đường dẫn tile",
      noFrames: "Chưa có khung nào (hoặc chưa nối database).",
    },

    tools: {
      inspect: "Xem một vị trí",
      placeholder: "Địa chỉ, link Google Maps hoặc tọa độ",
      pickPlace: "Nhập một vị trí để xem radar và dự báo.",
      view: "Xem",
      busy: "Đang tìm…",
      notFound: "Không tìm thấy địa điểm.",
      radarTitle: "Ảnh radar và chuyển động",
      radarAlt: "Radar quanh vị trí",
      legend: "Chấm đỏ: vị trí · vòng 25/50/100 km · mũi tên: quãng đường mưa đi trong 60 phút · lưới: ranh giới tile z7",
      detail: "Dự báo chi tiết",
      place: "Vị trí",
      frame: "Khung radar",
      now: "Hiện tại",
      rainIn: "Mưa tới sau",
      heavyIn: "Mưa to sau",
      motion: "Chuyển động",
      motionValue: (kmh: string, dir: string, deg: string) => `${kmh} km/h về ${dir} (${deg}°)`,
      noMotion: "không đủ dữ liệu",
      coherence: "Độ đồng hướng",
      gain: "Vector giải thích radar",
      member: (method: string) => `Chuyển động ${method.toUpperCase()}`,
      threshold: "Ngưỡng",
      thresholdValue: (rain: number, heavy: number) => `mưa ≥ ${rain} dBZ · mưa to ≥ ${heavy} dBZ`,
      minuteFromFrame: "Phút (từ khung)",
      predictedDbz: "dBZ dự báo",
    },
  },
};

export type Dict = typeof vi;
