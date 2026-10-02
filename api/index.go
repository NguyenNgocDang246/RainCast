// Package handler is the forecast API as a Vercel Go function; vercel.json
// sends every /api/* request here. Unlike cmd/raincast it runs nothing in
// the background: the frame index reloads on demand, browsers download the
// radar tiles (server.Config.ClientTiles) and forecasts are shared through
// Redis when REDIS_URL is set.
//
// It needs no database and serves no admin page. Environment: REDIS_URL
// (optional), GEOAPIFY_KEY, CORS_ORIGIN (the web app's origin), APP_ENV
// (production unless "development": debug logs).
package handler

import (
	"context"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"raincast/app"
	"raincast/internal/env"
	"raincast/internal/geocode"
	"raincast/internal/pipeline"
	"raincast/internal/server"
)

var (
	once      sync.Once
	api       http.Handler
	geo       *geocode.Client
	initErr   error
	lastPrune atomic.Int64
	prod      = env.Production(true)
	log       = env.Logger(true, !prod) // JSON for Vercel's logs; debug outside production
)

func setup() {
	log.Info("environment", "app_env", env.Name(prod))
	cfg := pipeline.DefaultConfig()
	// No poller: a request reloads an index older than this. RainViewer
	// adds a frame every 10 minutes.
	cfg.IndexMaxAge = 2 * time.Minute

	geo = geocode.New(os.Getenv("GEOAPIFY_KEY"), "raincast/1.0", "vn")
	geo.Bias = &geocode.LatLon{Lat: cfg.Stations[0].Lat, Lon: cfg.Stations[0].Lon}
	// Vercel's CDN keeps map tiles (s-maxage); /tmp only helps a warm
	// instance and is small.
	geo.TileStyle, geo.TileDir, geo.TileAge = "osm-liberty", "/tmp/maptiles", 15*24*time.Hour
	geo.TileMaxBytes = 200 << 20
	geo.Log = log

	a, err := app.New(context.Background(), app.Options{
		Pipeline: cfg,
		RedisURL: os.Getenv("REDIS_URL"),
		Geocode:  geo,
		Server: server.Config{
			CORSOrigin:    os.Getenv("CORS_ORIGIN"),
			ClientTiles:   true,
			CountryHeader: "X-Vercel-IP-Country",
			RealIPHeader:  "X-Real-IP",
		},
	}, log)
	if err != nil {
		initErr = err
		return
	}
	api = a.Handler
}

// Handler serves every /api/* request.
func Handler(w http.ResponseWriter, r *http.Request) {
	once.Do(setup)
	if initErr != nil {
		log.Error("setup", "err", initErr)
		http.Error(w, `{"error":"server is not configured"}`, http.StatusInternalServerError)
		return
	}
	// The map tile cache in /tmp is pruned at most hourly, in a request
	// since nothing runs between them.
	if now, last := time.Now().Unix(), lastPrune.Load(); now-last > 3600 && lastPrune.CompareAndSwap(last, now) {
		if err := geo.PruneTiles(); err != nil {
			log.Warn("prune map tiles", "err", err)
		}
	}
	api.ServeHTTP(w, r)
}
