# RainCast web

Giao diện Next.js của RainCast: tìm địa điểm, bản đồ radar và dự báo mưa 60 phút tới. Mọi dự báo do backend Go tính (`cmd/raincast` khi chạy local, `api/index.go` trên Vercel).

## Chạy local

```powershell
# Ở thư mục gốc repo: backend nghe ở :8080
go run ./cmd/raincast

# Ở thư mục web/
npm install
npm run dev
```

Mở http://localhost:3000 (trang admin: http://localhost:3000/admin, chỉ có khi chạy local).

## Biến môi trường

| Biến | Dùng khi | Ý nghĩa |
|---|---|---|
| `API_URL` | `next dev` | Backend mà `/api/*` được chuyển tới, mặc định `http://localhost:8080` |
| `NEXT_PUBLIC_API_BASE` | build trên Vercel | URL của project API; trình duyệt gọi thẳng tới đó. Để trống thì gọi cùng origin |

## Lệnh

| Lệnh | Việc |
|---|---|
| `npm run dev` | Server dev |
| `npm run build` | Build production |
| `npm run lint` | ESLint |

Tham số backend và các lệnh thu dữ liệu, backtest: xem [COMMANDS.md](../COMMANDS.md).
