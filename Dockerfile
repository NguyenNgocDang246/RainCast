# Backend: the Go server (cmd/raincast). Built by docker-compose.yml.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
# modernc.org/sqlite is pure Go, so no cgo is needed.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/raincast ./cmd/collect ./cmd/backtest

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/ /usr/local/bin/
# SQLite database, tile cache and backtest.json live here (a volume).
VOLUME /app/data
EXPOSE 8080
# Same origin behind Caddy, so CORS is off.
ENTRYPOINT ["raincast", "-addr", ":8080", "-cors", ""]
