# Lệnh và tham số

RainCast có ba chương trình, đều chạy từ thư mục gốc repo:

| Lệnh | Việc |
|---|---|
| `go run ./cmd/raincast` | Server HTTP: dự báo mưa cho một vị trí, trang admin |
| `go run ./cmd/collect` | Thu radar các vùng đang mưa trên thế giới để backtest |
| `go run ./cmd/backtest` | Chấm điểm các phương pháp dự báo trên dữ liệu collect đã thu |

Cả ba đọc `.env` khi khởi động. Xem đầy đủ tham số của một lệnh bằng `-h`, ví dụ `go run ./cmd/backtest -h`.

## Biến môi trường

| Biến | Dùng bởi | Ý nghĩa |
|---|---|---|
| `DATABASE_URL` | cả ba | PostgreSQL chung: danh sách frame và vùng collect đã thu |
| `REDIS_URL` | raincast | Redis cache dự báo giữa các tiến trình |
| `GEOAPIFY_KEY` | raincast | Khoá Geoapify cho tìm địa chỉ và bản đồ |
| `CORS_ORIGIN` | raincast | Origin được phép gọi API |
| `APP_ENV` | raincast | `production` (hoặc `prod`) bật chế độ chạy thật |

## Giới hạn tài nguyên (guard)

Cả ba lệnh có chung bộ tham số này: vượt giới hạn là tiến trình tự dừng, để không làm treo máy.

| Tham số | Mặc định | Ý nghĩa |
|---|---|---|
| `-max-cpu` | 50 (backtest: 65) | Dừng khi tiến trình dùng quá % tổng CPU này trong 15 giây (0 = không giới hạn) |
| `-max-memory-mb` | 2048 | Dừng khi tiến trình dùng quá số MB RAM này |
| `-max-procs` | 4 | Số core Go được dùng cùng lúc (0 = tất cả) |
| `-max-tiles-gb` | 20 | Dừng khi thư mục tile vượt số GB này |
| `-min-free-disk-gb` | 20 | Dừng khi ổ chứa dữ liệu còn trống ít hơn số GB này |
| `-min-free-ram` | 15 | Dừng khi RAM trống của máy dưới % này |
| `-stop-on-battery` | collect: bật; raincast, backtest: tắt | Dừng khi laptop chuyển sang chạy pin |

Với tham số dạng bật/tắt, tắt bằng `-ten=false`, ví dụ `-stop-on-battery=false`.

## raincast

Server dự báo. Mặc định nghe ở `:8080`.

```powershell
go run ./cmd/raincast
```

**Server**

| Tham số | Mặc định | Ý nghĩa |
|---|---|---|
| `-addr` | `:8080` | Địa chỉ lắng nghe HTTP |
| `-cors` | `http://localhost:3000` | Origin CORS được phép (rỗng để tắt; mặc định không có khi `APP_ENV=production`) |
| `-database-url` | `$DATABASE_URL` | PostgreSQL của collect, cho danh sách frame ở trang admin (rỗng: không dùng) |
| `-redis` | `$REDIS_URL` | Redis cache dự báo giữa các tiến trình (rỗng: chỉ cache trong bộ nhớ) |
| `-backtest-report` | `data/backtest.json` | Report backtest hiển thị ở trang admin |
| `-debug` | tắt | Log chi tiết |

**Radar**

| Tham số | Mặc định | Ý nghĩa |
|---|---|---|
| `-rate-limit` | 80 | Số request RainViewer mỗi phút. RainViewer cho 100/phút mỗi IP, dùng chung với collect nếu cùng IP |
| `-poll` | 2m | Bao lâu kiểm tra frame mới một lần |
| `-cache` | `data/tiles-live` | Thư mục cache tile cho dự báo trực tiếp (tách riêng với `data/tiles` của collect) |
| `-cache-age` | 3h | Thời gian giữ tile trực tiếp |
| `-client-tiles` | tắt | Trình duyệt tự tải tile radar thay cho server (như khi chạy trên Vercel) |

**Mô hình dự báo**

| Tham số | Mặc định | Ý nghĩa |
|---|---|---|
| `-model` | `ensemble` | `ensemble` (trung bình Lucas–Kanade + Horn–Schunck + TREC) hoặc một phương pháp: `trec`, `hs`, `lk` |
| `-motion-pairs` | 4 | Số cặp frame (10 phút mỗi cặp) dùng để ước lượng chuyển động |
| `-trend` | bật | Cho echo mạnh lên hoặc yếu đi khi di chuyển (trạm luôn ghi cả bản có xu hướng) |
| `-ml` | bật | Hiệu chỉnh dự báo bằng mô hình ML nhúng trong binary (`internal/mlmodel`); chỉ có tác dụng với `-model ensemble` và `-trend` |
| `-threshold` | 20 | Ngưỡng dBZ tính là có mưa |
| `-likely` | 30 | Dưới mức dBZ này chỉ báo "có thể mưa" |
| `-heavy` | 40 | Ngưỡng dBZ tính là mưa to |
| `-stations` | 10 trạm quanh TP.HCM | File JSON các trạm `[{"id","name","lat","lon"}]` để dự báo và kiểm chứng |

**Tìm địa chỉ và bản đồ**

| Tham số | Mặc định | Ý nghĩa |
|---|---|---|
| `-geocode-countries` | `vn` | Mã quốc gia (cách nhau bằng dấu phẩy) ưu tiên khi tìm địa chỉ nếu không biết người dùng ở nước nào |
| `-geocode-ua` | `raincast/1.0` | User-Agent gửi tới Geoapify và khi mở link bản đồ rút gọn |
| `-geoip-db` | `geoip/country.mmdb` | CSDL IP → quốc gia (định dạng MaxMind, ví dụ DB-IP Lite); thiếu thì dùng `-geocode-countries` |
| `-map-style` | `osm-liberty` | Kiểu bản đồ Geoapify |
| `-map-cache` | `data/maptiles` | Thư mục cache tile bản đồ (rỗng để tắt) |
| `-map-cache-age` | 360h (15 ngày) | Thời gian giữ tile bản đồ (0 = giữ mãi) |
| `-map-cache-mb` | 1024 | Dung lượng tối đa cache bản đồ, xoá tile cũ nhất trước (0 = không giới hạn) |

## collect

Thu radar cho backtest: luôn giữ các vùng đang mưa nhiều nhất có radar phủ. Chạy liên tục càng lâu càng tốt, vì mỗi đoạn liên tục mất 10 frame đầu–cuối không dùng được (xem [Thời lượng chạy](#thời-lượng-chạy-collect)).

```powershell
go run ./cmd/collect
```

| Tham số | Mặc định | Ý nghĩa |
|---|---|---|
| `-database-url` | `$DATABASE_URL` | PostgreSQL lưu frame và vùng, dùng chung với backtest |
| `-rate-limit` | 90 | Số request RainViewer mỗi phút. Hạ xuống nếu raincast chạy cùng IP (tổng hai bên phải dưới 100) |
| `-regions` | 30 | Số vùng mưa thu cùng lúc trên toàn thế giới |
| `-poll` | 2m | Bao lâu kiểm tra frame mới một lần |
| `-cache` | `data/tiles` | Thư mục lưu tile, backtest đọc từ đây |
| `-cache-age` | 1440h (60 ngày) | Thời gian giữ tile cho backtest |
| `-data` | `data` | Nơi lưu điểm đã chấm |
| `-keep-awake` | bật | Không cho Windows tự sleep khi đang chạy, để dữ liệu không bị đứt |
| `-sat-cache` | rỗng | Thư mục lưu ảnh vệ tinh Himawari (B13, JMA) cho các vùng vệ tinh nhìn thấy (khoảng 75°E–155°W), ví dụ `data/himawari`. Mặc định không thu: vệ tinh thêm chưa tới 1 điểm CSI cho ML (xem [Huấn luyện ML](#huấn-luyện-ml)). Phải tách khỏi `-cache` |
| `-sat-rate` | 1 | Số request JMA mỗi giây. Ảnh vệ tinh tải trên luồng riêng, không làm chậm radar |
| `-debug` | tắt | Log chi tiết |

Cùng các tham số [guard](#giới-hạn-tài-nguyên-guard). Khi chạy cùng lúc với backtest, có thể cần nới `-min-free-ram` (ví dụ `-min-free-ram 5`), vì backtest tốn nhiều RAM có thể làm collect bị dừng.

## backtest

Chấm điểm các phương pháp dự báo trên frame collect đã thu, ghi report cho trang admin. Mỗi lần chạy chỉ chấm thêm các thời điểm mới; điểm cũ lưu ở `data/backtest_state.gob`. Khi danh sách biến thể hay cách chấm đổi, lần chạy sau tự chấm lại từ đầu.

```powershell
go run ./cmd/backtest
```

**Chấm điểm**

| Tham số | Mặc định | Ý nghĩa |
|---|---|---|
| `-database-url` | `$DATABASE_URL` | PostgreSQL của collect |
| `-cache` | `data/tiles` | Thư mục tile của collect |
| `-data` | `data` | Nơi lưu điểm đã chấm |
| `-out` | `data/backtest.json` | File report cho trang admin (rỗng để không ghi) |
| `-fresh` | tắt | Chấm lại toàn bộ thay vì cộng thêm vào điểm đã lưu |
| `-days` | 0 | Đi cùng `-fresh`: chỉ chấm frame trong N ngày gần nhất (0 = tất cả) |
| `-since` | rỗng | Chỉ chấm các thời điểm dự báo từ mốc này (unix giây hoặc RFC 3339); frame trước đó vẫn dùng làm lịch sử. Dùng cùng mốc với `-train-until` của `train.py` |
| `-step` | 8 | Khoảng cách giữa các điểm mẫu, tính bằng pixel (~1,2 km mỗi pixel) |
| `-parallel` | 0 | Số vùng chấm cùng lúc (0 = theo số core) |
| `-workers` | 0 | Số goroutine cho mỗi trường chuyển động (0 = theo số core) |
| `-cpuprofile` | rỗng | Ghi CPU profile để xem bằng `go tool pprof` |

**ML** (xem [Huấn luyện ML](#huấn-luyện-ml))

| Tham số | Mặc định | Ý nghĩa |
|---|---|---|
| `-ml-dir` | `internal/ml/models` | Mô hình do `scripts/ml/train.py` xuất. Có bộ đặc trưng nào thì chấm thêm biến thể ML tương ứng ("ML", "Ensemble hiệu chỉnh"; và "ML + NWP + vệ tinh", "ML + vệ tinh", "ML + NWP" khi bật nguồn tương ứng) |
| `-dump-features` | rỗng | Đi cùng `-fresh`: ghi dữ liệu huấn luyện vào file này (float32), kèm file `.json` mô tả cột |
| `-dump-every` | 2 | Đi cùng `-dump-features`: cứ N điểm mẫu theo mỗi chiều thì ghi 1 điểm |
| `-sat-cache` | rỗng | Ảnh vệ tinh đã thu (collect, satfill), ví dụ `data/himawari`. Mặc định không dùng; mô hình cần vệ tinh khi đó bị bỏ qua |
| `-nwp-cache` | rỗng | Cache Open-Meteo (CAPE, CIN…), ví dụ `data/nwp`. Mặc định không gọi Open-Meteo; mô hình cần NWP khi đó bị bỏ qua |

Chỉ khi bật hai tham số trên, và có `-dump-features` hoặc có mô hình, backtest mới đọc vệ tinh và gọi Open-Meteo.

**Kiểm tra và dọn dữ liệu** (không chấm điểm)

| Tham số | Ý nghĩa |
|---|---|
| `-inventory` | Báo cáo theo từng vùng: số frame thu được, số có đủ tile, các đoạn liên tục, số frame dùng được cho backtest |
| `-prune` | Liệt kê frame, tile, vùng không dùng được cho backtest (không đụng tới dữ liệu 3 giờ gần nhất). Chỉ liệt kê, không xoá |
| `-yes` | Đi cùng `-prune`: xoá thật |

Cùng các tham số [guard](#giới-hạn-tài-nguyên-guard).

**Ví dụ**

```powershell
# Chấm thêm các thời điểm mới (dùng thường xuyên)
go run ./cmd/backtest

# Chấm lại từ đầu, chỉ 7 ngày gần nhất
go run ./cmd/backtest -fresh -days 7

# Chạy nhẹ để không làm chậm collect đang chạy song song
go run ./cmd/backtest -parallel 4

# Xem collect đã thu được gì và bao nhiêu dùng được
go run ./cmd/backtest -inventory

# Xem rồi xoá dữ liệu không dùng được
go run ./cmd/backtest -prune
go run ./cmd/backtest -prune -yes
```

## satfill

Tải bù ảnh vệ tinh Himawari cho các frame radar đã thu, ở các vùng vệ tinh nhìn thấy. Chỉ cần khi muốn thử lại đặc trưng vệ tinh (mặc định không dùng, xem [Huấn luyện ML](#huấn-luyện-ml)). JMA chỉ giữ ảnh khoảng 5 ngày, nên lệnh tải ảnh cũ nhất trước. Ảnh đã có thì bỏ qua, nên dừng giữa chừng rồi chạy lại cũng được.

```powershell
go run ./cmd/satfill -dry-run   # chỉ đếm số tile cần tải
go run ./cmd/satfill
```

| Tham số | Mặc định | Ý nghĩa |
|---|---|---|
| `-days` | 6 | Chỉ frame trong N ngày gần nhất |
| `-rate` | 2 | Số request JMA mỗi giây |
| `-sat-cache` | `data/himawari` | Thư mục lưu ảnh, giống collect và backtest |
| `-margin` | 32 | Số pixel vệ tinh (~5 km) lấy thêm quanh mỗi vùng radar |

## Huấn luyện ML

Lớp ML học từ backtest khi nào mưa mạnh lên, yếu đi hay hình thành, dựa trên các thành viên ensemble, echo quanh điểm và phía gió tới, ô bão, giờ trong ngày. Chỉ huấn luyện trên máy dev; backtest đọc mô hình (file JSON) từ `-ml-dir`. App dùng bản `radar.json` nhúng trong binary (`internal/mlmodel/radar.json.gz`), nên deploy không cần Python hay file mô hình riêng.

```powershell
# 1. Xuất dữ liệu huấn luyện (chạy lâu như một lần -fresh)
go run ./cmd/backtest -fresh -dump-features data/features.bin

# 2. Huấn luyện bộ "radar" và "ens" (một lần: pip install -r scripts/ml/requirements.txt)
python scripts/ml/train.py data/features.bin

# 3. Chấm các biến thể ML (mỗi vùng dùng mô hình học từ các vùng còn lại)
go run ./cmd/backtest -fresh

# Kiểm tra nghiêm hơn: học trên phần đầu, chấm trên phần sau mà mô hình chưa thấy
python scripts/ml/train.py data/features.bin -train-until 2026-10-05T00:00:00Z
go run ./cmd/backtest -fresh -since 2026-10-05T00:00:00Z

# 4. Kiểm tra Go cho ra đúng xác suất của LightGBM
go test ./internal/ml
```

**NWP và vệ tinh mặc định tắt.** Chấm trên dữ liệu mô hình chưa thấy (sau 05/10/2026, mỗi vùng dùng mô hình của fold không chứa nó), bỏ NWP hay vệ tinh đều đổi CSI dưới 1 điểm, kể cả ở các vùng Himawari phủ; xáo trộn chúng cũng chỉ mất ≤ 0,01 CSI, trong khi các đặc trưng radar (thành viên ensemble, phía gió tới, quanh điểm) mất 0,1–0,4. Code vẫn giữ; muốn thử lại:

```powershell
go run ./cmd/collect -sat-cache data/himawari        # thu ảnh vệ tinh
go run ./cmd/backtest -fresh -dump-features data/features.bin -sat-cache data/himawari -nwp-cache data/nwp
python scripts/ml/train.py data/features.bin -sets radar,ens,all,no_nwp,no_sat
go run ./cmd/backtest -fresh -sat-cache data/himawari -nwp-cache data/nwp
```

Fold chia theo ô 15° kinh/vĩ độ (`ml.Fold`, `fold_of` trong `train.py`), để các vùng gần nhau — cùng cơn bão, cùng thời điểm — luôn nằm cùng một phía, mô hình không "nhìn trộm" được vùng nó đang được chấm. Biến thể "Ensemble hiệu chỉnh" chỉ thấy đầu ra của các thành viên (bộ `ens`), được hiệu chỉnh y như ML: so "Ensemble hiệu chỉnh → ML" mới cho biết các đặc trưng khác (quanh điểm, phía gió tới, bão…) thật sự thêm được gì, ngoài việc dời ngưỡng.

`train.py` ghi vào `internal/ml/models/`:
- `<bộ>_fold<k>.json`: dùng cho backtest.
- `<bộ>.json`: học trên mọi vùng. `radar.json` là bản app dùng, sau khi nén vào `internal/mlmodel` (xem dưới).
- `parity.json`: dòng mẫu để kiểm tra Go, theo bộ đầu tiên trong `-sets`.
- `summary.json`: CSI trên tập kiểm tra theo ngưỡng và lead.

Cần ít nhất 2–4 tuần dữ liệu thì kết quả mới đáng tin.

### Cập nhật mô hình cho app

App (cả `raincast` và Vercel) nạp `internal/mlmodel/radar.json.gz` khi khởi động (~0,2 giây, chạy song song với lần tải index đầu tiên). Sau khi train và backtest xác nhận mô hình mới tốt hơn, train lại bản cho app trên toàn bộ dữ liệu (không `-train-until`), rồi nén vào repo:

```powershell
python scripts/ml/train.py data/features.bin -sets radar
go test ./internal/ml        # Go cho ra đúng xác suất của LightGBM
gzip -9 -c internal/ml/models/radar.json > internal/mlmodel/radar.json.gz
go test ./internal/mlmodel
```

File `.gz` (~0,8 MB) được commit, khác với các file trong `internal/ml/models/`.

## Thời lượng chạy collect

Mỗi thời điểm dự báo trong backtest cần 11 frame liên tục (4 frame lịch sử + hiện tại + 6 mốc +10′ … +60′), tức 100 phút. Một đoạn liên tục N frame cho N − 10 thời điểm dùng được:

| Chạy liên tục | Dùng được |
|---|---|
| < 1 giờ 40 | 0% |
| 2 giờ | 23% |
| 6 giờ | 73% |
| 12 giờ | 86% |
| 24 giờ | 93% |

Mỗi lần khởi động, collect tải bù khoảng 2 giờ frame quá khứ, nên kể cả đợt chạy ngắn cũng thường có vài thời điểm dùng được. Để so sánh các phương pháp đáng tin cần ít nhất 30 đợt mưa, thường là 2–3 ngày chạy liên tục.
