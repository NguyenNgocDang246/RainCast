package app

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"raincast/internal/env"
	"raincast/internal/geocode"
	"raincast/internal/pipeline"
	"raincast/internal/server"
)

// Serverless is the API as a serverless function (api/index.go on Vercel).
// Unlike cmd/raincast it runs nothing in the background: the frame index
// reloads on demand, the radar tiles are downloaded by the server within a
// small budget and by browsers otherwise (server.Config.ClientTiles), and
// forecasts are shared through Redis when
// REDIS_URL is set. It needs no database and serves no admin page.
//
// Environment: GEOAPIFY_KEY, CORS_ORIGIN (the web app's origin), REDIS_URL
// (optional), APP_ENV (production unless "development": debug logs).
func Serverless() http.Handler {
	s := &serverless{prod: env.Production(true)}
	// JSON for Vercel's logs; debug outside production.
	s.log = env.Logger(true, !s.prod)
	return s
}

type serverless struct {
	prod bool
	log  *slog.Logger

	once      sync.Once
	api       http.Handler
	geo       *geocode.Client
	err       error
	lastPrune atomic.Int64
}

// setup builds the API on the first request of an instance.
func (s *serverless) setup() {
	log := s.log
	log.Info("environment", "app_env", env.Name(s.prod))
	cfg := pipeline.DefaultConfig()
	// No poller: a request reloads an index older than this. RainViewer
	// adds a frame every 10 minutes.
	cfg.IndexMaxAge = 2 * time.Minute
	// Vercel's outbound IPs share RainViewer's per-IP limit with other
	// sites: the server downloads a forecast's tiles while a 429 hasn't
	// told it to stop, and browsers download them otherwise.
	cfg.ServerFetchPerMinute = 60

	s.geo = geocode.New(os.Getenv("GEOAPIFY_KEY"), "raincast/1.0", "vn")
	s.geo.Bias = &geocode.LatLon{Lat: cfg.Stations[0].Lat, Lon: cfg.Stations[0].Lon}
	// Vercel's CDN keeps map tiles (s-maxage); /tmp only helps a warm
	// instance and is small.
	s.geo.TileStyle, s.geo.TileDir, s.geo.TileAge = "osm-liberty", "/tmp/maptiles", 15*24*time.Hour
	s.geo.TileMaxBytes = 200 << 20
	s.geo.Log = log

	a, err := New(context.Background(), Options{
		Pipeline: cfg,
		RedisURL: os.Getenv("REDIS_URL"),
		Geocode:  s.geo,
		ML:       true,
		Server: server.Config{
			CORSOrigin:    os.Getenv("CORS_ORIGIN"),
			ClientTiles:   true,
			CountryHeader: "X-Vercel-IP-Country",
			RealIPHeader:  "X-Real-IP",
		},
	}, log)
	if err != nil {
		s.err = err
		return
	}
	s.api = a.Handler
}

func (s *serverless) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.once.Do(s.setup)
	if s.err != nil {
		s.log.Error("setup", "err", s.err)
		http.Error(w, `{"error":"server is not configured"}`, http.StatusInternalServerError)
		return
	}
	// The map tile cache in /tmp is pruned at most hourly, in a request
	// since nothing runs between them.
	if now, last := time.Now().Unix(), s.lastPrune.Load(); now-last > 3600 && s.lastPrune.CompareAndSwap(last, now) {
		if err := s.geo.PruneTiles(); err != nil {
			s.log.Warn("prune map tiles", "err", err)
		}
	}
	s.api.ServeHTTP(w, r)
}
