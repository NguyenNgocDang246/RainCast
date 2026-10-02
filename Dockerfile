# The collector machine's image: cmd/collect (the default), cmd/backtest and
# cmd/migrate-pg. The API runs on Vercel (api/index.go). Built by
# docker-compose.yml.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
# Pure Go (pgx; modernc.org/sqlite only for migrate-pg), so no cgo is needed.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/collect ./cmd/backtest ./cmd/migrate-pg

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/ /usr/local/bin/
# The tile cache and the backtest's saved scores live here (a volume); the
# database is PostgreSQL (DATABASE_URL).
VOLUME /app/data
ENTRYPOINT ["collect"]
