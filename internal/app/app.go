// Package app wires the forecast API together: the database, the shared
// cache, the radar pipeline, geocoding and the HTTP server. cmd/raincast
// runs it as a long-lived server; api/index.go runs it on Vercel.
package app

import (
	"context"
	"log/slog"
	"net/http"

	"raincast/internal/backtest"
	"raincast/internal/cache"
	"raincast/internal/geocode"
	"raincast/internal/geoip"
	"raincast/internal/pipeline"
	"raincast/internal/rainviewer"
	"raincast/internal/server"
	"raincast/internal/store"
)

// Options configures App.
type Options struct {
	Pipeline pipeline.Config
	// DatabaseURL is cmd/collect's database, for the admin page's frame
	// list (empty: none; production needs no database).
	DatabaseURL string
	// BacktestReport is the file cmd/backtest writes (empty: none).
	BacktestReport string
	RedisURL       string // empty: forecasts are cached in this process only
	// CacheDir keeps downloaded radar tiles (empty: none).
	CacheDir  string
	RateLimit int // RainViewer requests per minute
	// Geocode is the address search and map tile client, set up by the
	// caller (key, style, tile cache).
	Geocode *geocode.Client
	// GeoIPDB is the IP-to-country database (empty: none).
	GeoIPDB string
	Server  server.Config
}

// App is a ready API.
type App struct {
	Handler  http.Handler
	Pipeline *pipeline.Pipeline
	Store    *store.Store
}

// New connects to the database, when there is one, and the shared cache
// and builds the API.
func New(ctx context.Context, o Options, log *slog.Logger) (*App, error) {
	var st *store.Store
	if o.DatabaseURL != "" {
		var err error
		if st, err = store.Open(o.DatabaseURL); err != nil {
			return nil, err
		}
	}
	o.Pipeline.Shared = cache.Open(ctx, o.RedisURL, log)
	client := rainviewer.New(o.CacheDir)
	if o.RateLimit > 0 {
		client.SetRateLimit(o.RateLimit)
	}
	p := pipeline.New(o.Pipeline, client, log)

	cfg := o.Server
	if o.GeoIPDB != "" {
		gip, err := geoip.Open(o.GeoIPDB, log)
		if err != nil {
			// It loads once the file appears; until then search favors the
			// configured countries.
			log.Info("geoip not loaded", "err", err)
		}
		cfg.CountryOf = gip.Country
	}
	if o.BacktestReport != "" {
		cfg.Backtest = func() (*backtest.Report, error) { return backtest.ReadReport(o.BacktestReport) }
	}
	return &App{Handler: server.New(p, o.Geocode, st, log, cfg), Pipeline: p, Store: st}, nil
}

// Close releases the database.
func (a *App) Close() error {
	if a.Store == nil {
		return nil
	}
	return a.Store.Close()
}
