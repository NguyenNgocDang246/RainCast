// Vietnamese UI text. en.ts must match this shape (see Dict).

export const vi = {
  meta: {
    title: "Raincast – Mưa còn bao lâu nữa?",
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
    "could not load radar data for this location": "Không tải được radar cho vị trí này.",
  } as Record<string, string>,

  dashboard: {
    loading: "Đang xem radar…",
    footerAddress: "Địa chỉ & bản đồ",
  },

  intro: {
    /** The middle part is highlighted. */
    heading: ["Mưa còn ", "bao lâu nữa", " tới chỗ bạn?"],
    lead: "Xem trước cơn mưa trong một giờ tới, ngay tại nơi bạn đứng.",
  },

  map: {
    label: "Bản đồ chọn vị trí",
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
    moving: (dir: string, kmh: string) => ` · mưa đang di chuyển về hướng ${dir}, ${kmh} km/h`,
    about: (clock: string) => `Khoảng ${clock}`,

    heavyNow: "Đang mưa to",
    heavyContinues: "Mưa to còn kéo dài",
    easingNow: "Đang dịu bớt",
    easingIn: "Dịu bớt sau",
    noEasing: "Chưa thấy dấu hiệu ngớt trong 60 phút tới",

    maybeLightNow: "Có thể đang mưa nhẹ",
    lightNow: "Đang mưa nhẹ",
    heavySoon: "Sắp mưa to",
    heavyIn: "Mưa to sau",
    noHeavy: "Chưa có mưa to",
    lightLasts: "Mưa nhẹ có thể kéo dài hơn 60 phút",
    mayStop: (clock: string) => `Có thể tạnh khoảng ${clock}`,

    maybeSoon: "Có thể sắp mưa",
    maybeIn: "Có thể mưa sau",
    drizzle: (clock: string) => `Khoảng ${clock}, mưa rất nhẹ hoặc lất phất`,
    soon: "Sắp mưa",
    rainIn: "Mưa sau",
    heavyAfter: (n: number, clock: string) => `Mưa to sau khoảng ${n} phút (${clock})`,
    moderate: (clock: string) => `Khoảng ${clock}, mưa vừa`,

    dry: "Trời tạm ổn",
    noRain: "Không mưa trong 60 phút tới",
    noRainDetail: "Radar chưa thấy cơn mưa nào đang tiến về chỗ bạn",
  },

  admin: {
    tabs: {
      overview: "Tổng quan",
      accuracy: "Độ chính xác",
      issues: "Lịch sử dự báo",
      lookups: "Lượt tra cứu",
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

      stored: "Dữ liệu đã lưu",
      frames: "Khung radar",
      atStations: (n: number) => `đo tại ${n} trạm`,
      issues: "Lần dự báo (điểm theo dõi)",
      forecasts: "Dự đoán theo mốc",
      verified: "Đã đối chiếu",
      lookups: "Lượt tra cứu của người dùng",

      geocoding: (on: boolean) => `Geoapify (từ lần khởi động) · ${on ? "đã có key" : "chưa có key"}`,
      cacheHits: "Trả từ cache",
      failed: "Lỗi",
      rateLimited: (n: number) => `${n} lần hết lượt`,
      tiles: "Ô bản đồ tải về",
      tilesSub: (cached: number, errors: number) => `${cached} từ cache · ${errors} lỗi`,

    },

    accuracy: {
      series: {
        model: "Mô hình",
        persistence: "Giữ nguyên (baseline)",
        trend: "Mô hình + xu hướng",
      },
      metrics: {
        csi: "CSI",
        accuracy: "Chính xác",
        pod: "Bắt được mưa",
        far: "Báo động giả",
      },
      csiModel: "CSI · mô hình",
      csiTrend: "CSI · mô hình + xu hướng",
      csiBaseline: "CSI · baseline",
      accuracyPct: (p: string) => `chính xác ${p}`,
      noData: "chưa có dữ liệu",
      arrivalError: "Sai số giờ mưa tới",
      noArrivals: "chưa có lần mưa tới nào",
      arrivalBias: (n: number, late: boolean, mins: string) =>
        `${n} lần · ${late ? "mưa tới trễ hơn" : "mưa tới sớm hơn"} dự báo ${mins} ph`,
      byLeadShadow: (n: number) => `So sánh theo mốc · ${n} dự đoán có cả hai phiên bản`,
      byLead: (n: number) => `Theo mốc dự báo · ${n} dự đoán`,
      nothingYet: "Chưa có dự báo nào đủ thời gian để đối chiếu.",
      chartNote: (n: number) =>
        `Dữ liệu thật từ ${n} lần dự báo của các trạm, chấm khi khung radar thật về tới. “Xu hướng” cho vùng mưa mạnh lên hoặc yếu đi theo đà 20 phút gần nhất; nó chạy song song để so sánh, người dùng chưa thấy.`,
      table: (n: number) => `Bảng chi tiết · toàn bộ ${n} dự đoán đã đối chiếu`,
      lead: "Mốc",
      count: "Số lần",
      podFull: "Bắt được mưa (POD)",
      farFull: "Báo động giả (FAR)",
      contingency: "Trúng / trượt / giả / đúng-không-mưa",
      maeDbz: "Sai số dBZ",
      tableNote: "Số nhỏ là baseline “trời giữ nguyên”. CSI = trúng / (trúng + trượt + giả), chỉ số chính để tối ưu.",
      metricGroup: "Chỉ số",
      chartLabel: "Chỉ số theo mốc dự báo",
      tooltipCases: (n: number, c: string) => `${n} lần · trúng/trượt/giả/đúng: ${c}`,
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
      group: "Nhóm",
      regionsCol: "Vùng",
      eventsCol: "Đợt mưa",
      baselineCsi: "CSI giữ nguyên",
      climates: { tropical: "nhiệt đới", subtropical: "cận nhiệt", midlat: "ôn đới" } as Record<string, string>,
      bestCol: "Cấu hình tốt nhất",
      config: "Cấu hình",
      csiTotal: "CSI tổng",
      best: "tốt nhất",
      baseline: "Giữ nguyên (baseline)",
      variant: (pairs: number, trend: boolean) => `${pairs} cặp${trend ? " + xu hướng" : ""}`,
      methods: {
        trec: "TREC",
        cotrec: "COTREC",
        hs: "Horn–Schunck",
        lk: "Lucas–Kanade",
        "cell-nn": "Cell NN",
        "cell-hung": "Cell Hungarian",
        hybrid: "Hybrid",
        ensemble: "Ensemble trung bình",
        vote: "Ensemble bỏ phiếu",
      } as Record<string, string>,
      pairsSuffix: (pairs: number) => ` ${pairs} cặp`,
      trendSuffix: " + xu hướng",
      csiCi: "CSI tổng [95%]",
      delta: "Δ so với TREC 4 cặp",
      bss: "BSS",
      auc: "AUC",
      ms: "ms/lần",
      better: "chắc chắn tốt hơn",
      worse: "chắc chắn kém hơn",
      intervals: (blocks: number) => `Khoảng tin cậy 95% lấy từ ${blocks} khối 6 giờ.`,
      note: "Δ: chênh CSI so với TREC 4 cặp (cấu hình app đang dùng); ▲/▼ khi cả khoảng tin cậy nằm trên/dưới 0, tức khác biệt không phải do ngẫu nhiên. BSS: kỹ năng dự báo xác suất so với giữ nguyên (0 = không hơn, càng gần 1 càng tốt). AUC: khả năng phân biệt có mưa/không mưa (0,5 = đoán bừa, 1 = hoàn hảo). ms/lần: thời gian tính ước chừng. “+ xu hướng” thêm mưa mạnh lên/yếu đi. Mọi cấu hình chấm trên cùng thời điểm và điểm (chỉ nơi có radar phủ); điểm được cộng dồn qua các lần chạy.",
    },

    history: {
      issuesTitle: (n: number) => `Lịch sử dự báo của các trạm (${n} lần gần nhất)`,
      frame: "Khung radar",
      station: "Trạm",
      then: "Lúc đó",
      rainIn: "Mưa tới sau",
      heavyIn: "Mưa to sau",
      predVsObs: (m: number) => `+${m}′ dự / thật`,
      issuesNote:
        "dBZ dự báo / dBZ đo được khi khung thật về (… = chưa tới giờ). “đúng/sai” so mưa (≥ ngưỡng) với không mưa.",
      framesTitle: (n: number) => `Khung radar đã ghi (${n}) · dBZ đo tại từng trạm`,
      frameTime: "Thời điểm khung",
      recordedAt: "Ghi lúc",
      framesNote: "≥ 20 dBZ là mưa, ≥ 40 là mưa to; “–” là không có mưa hoặc chưa đo.",
      lookupsTitle: (n: number) => `Lượt tra cứu của người dùng (${n} gần nhất)`,
      at: "Lúc",
      place: "Vị trí",
      result: "Kết quả",
      motion: "Di chuyển",
      viewRadar: "Xem radar",
      noLookups: "Chưa có lượt tra cứu nào.",
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
      threshold: "Ngưỡng",
      thresholdValue: (rain: number, heavy: number) => `mưa ≥ ${rain} dBZ · mưa to ≥ ${heavy} dBZ`,
      minuteFromFrame: "Phút (từ khung)",
      predictedDbz: "dBZ dự báo",
    },
  },
};

export type Dict = typeof vi;
