// Command raincast predicts when rain will reach a location from RainViewer
// radar frames and serves the forecast and its track record over HTTP.
// Collecting data for backtesting (cmd/collect) and scoring methods
// (cmd/backtest) are separate commands; the admin page shows the report
// cmd/backtest last wrote.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"raincast/internal/backtest"
	"raincast/internal/geocode"
	"raincast/internal/geoip"
	"raincast/internal/guard"
	"raincast/internal/model"
	"raincast/internal/pipeline"
	"raincast/internal/rainviewer"
	"raincast/internal/server"
	"raincast/internal/store"
)

func main() {
	// Secrets such as GEOAPIFY_KEY come from the environment or .env.
	if err := loadDotEnv(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "load .env:", err)
		os.Exit(1)
	}
	cfg := pipeline.DefaultConfig()
	var threshold, likely, heavy float64
	stationsFile := flag.String("stations", "", `JSON file of stations [{"id","name","lat","lon"}] to forecast and verify (default: 10 around Ho Chi Minh City)`)
	flag.Float64Var(&threshold, "threshold", float64(cfg.Threshold), "dBZ counted as rain")
	flag.Float64Var(&likely, "likely", float64(cfg.Likely), "dBZ below which rain is reported as only possible")
	flag.Float64Var(&heavy, "heavy", float64(cfg.Heavy), "dBZ counted as heavy rain")
	modelName := flag.String("model", "ensemble", "forecast model: ensemble (Lucas–Kanade + Horn–Schunck + TREC, the default) or one method: "+strings.Join(model.Methods, ", "))
	motionPairs := flag.Int("motion-pairs", cfg.Model.Members[0].Pairs, "frame pairs (10 min each) each motion estimate averages")
	useTrend := flag.Bool("trend", cfg.Model.Trend, "let echoes grow or weaken as they travel (the trend version is always recorded for stations)")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "data/raincast.db", "SQLite database path")
	cacheDir := flag.String("cache", "data/tiles-live", "radar tile cache directory (only for live forecasts; cmd/collect keeps its own in data/tiles)")
	poll := flag.Duration("poll", 2*time.Minute, "how often to check for new frames")
	cors := flag.String("cors", "http://localhost:3000", "allowed CORS origin (empty to disable)")
	debug := flag.Bool("debug", false, "verbose logging")
	geoUA := flag.String("geocode-ua", "raincast/1.0", "User-Agent sent to Geoapify and when expanding short map links")
	mapStyle := flag.String("map-style", "osm-liberty", "Geoapify map tile style (see https://apidocs.geoapify.com/docs/maps/map-tiles/)")
	mapCache := flag.String("map-cache", "data/maptiles", "map tile cache directory (empty to disable)")
	mapCacheAge := flag.Duration("map-cache-age", 15*24*time.Hour, "how long cached map tiles are kept (0 keeps them forever)")
	mapCacheMB := flag.Int64("map-cache-mb", 1024, "size cap of the map tile cache in MB; the oldest tiles go first (0 for no cap)")
	geoCountries := flag.String("geocode-countries", "vn", "comma-separated country codes ranked first in address search when the user's country is unknown")
	geoipDB := flag.String("geoip-db", "geoip/country.mmdb", "IP-to-country database (MaxMind format, e.g. DB-IP Lite) naming the user's country, ranked first in address search (missing: -geocode-countries is used)")
	// Live tiles are only read again while they are among a forecast's
	// history frames; cmd/collect keeps the backtest's tiles.
	cfg.CacheAge = 3 * time.Hour
	flag.DurationVar(&cfg.CacheAge, "cache-age", cfg.CacheAge, "how long live radar tiles are kept")
	rateLimit := flag.Int("rate-limit", 80, "RainViewer requests per minute for this process; RainViewer allows 100 per IP, shared with cmd/collect")
	// The guard stops the process before it overloads the machine. A web
	// server keeps serving on battery; only cmd/collect stops then.
	def := guard.DefaultLimits("data", "data/tiles-live")
	def.StopOnBattery = false
	limits := guard.Flags(flag.CommandLine, def)
	flag.Parse()
	lim := limits()
	cfg.Threshold = float32(threshold)
	cfg.Heavy = float32(heavy)
	cfg.Likely = float32(likely)
	if *motionPairs < 1 {
		fmt.Fprintln(os.Stderr, "-motion-pairs must be at least 1")
		os.Exit(2)
	}
	m, err := model.Parse(*modelName, *motionPairs, *useTrend)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	cfg.Model = m
	// Keep at least the frames one forecast reads (pairs+1), with a margin.
	for _, mm := range m.Members {
		cfg.CacheAge = max(cfg.CacheAge, time.Duration(mm.Pairs+3)*10*time.Minute)
	}
	if *stationsFile != "" {
		st, err := pipeline.LoadStations(*stationsFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		cfg.Stations = st
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	geo := geocode.New(os.Getenv("GEOAPIFY_KEY"), *geoUA, *geoCountries)
	geo.Bias = &geocode.LatLon{Lat: cfg.Stations[0].Lat, Lon: cfg.Stations[0].Lon}
	geo.TileStyle, geo.TileDir, geo.TileAge = *mapStyle, *mapCache, *mapCacheAge
	geo.TileMaxBytes = *mapCacheMB << 20
	geo.Log = log
	if geo.Key == "" {
		// Coordinates and map links still work; addresses and the map do not.
		log.Warn("GEOAPIFY_KEY is not set: address search and map tiles are off")
	}
	gip, err := geoip.Open(*geoipDB, log)
	if err != nil {
		// It loads once the geoip service writes the file; until then search
		// favors -geocode-countries.
		log.Info("geoip not loaded", "err", err)
	}
	log.Info("config", "stations", len(cfg.Stations), "model", cfg.Model.Name, "trend", cfg.Model.Trend, "geoapify", geo.Key != "")
	lim.DiskPath, lim.DirPath = filepath.Dir(*dbPath), *cacheDir
	if err := run(cfg, lim, *rateLimit, geo, gip, *addr, *dbPath, *cacheDir, *poll, *cors, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfg pipeline.Config, lim guard.Limits, rateLimit int, geo *geocode.Client, gip *geoip.DB, addr, dbPath, cacheDir string, poll time.Duration, cors string, log *slog.Logger) error {
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Everything long-running derives from the guard's context, so crossing
	// a resource limit stops all of it.
	ctx, _ := guard.Start(sigCtx, lim, log)
	lim.Log(log)

	st, err := store.Open(dbPath, float64(cfg.Threshold))
	if err != nil {
		return err
	}
	defer st.Close()

	client := rainviewer.New(cacheDir)
	client.SetRateLimit(rateLimit)
	p := pipeline.New(cfg, client, log)
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(ctx, poll)
	}()
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if err := geo.PruneTiles(); err != nil {
				log.Warn("prune map tiles", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	srv := &http.Server{
		Addr: addr,
		Handler: server.New(p, geo, st, log, server.Config{
			CORSOrigin: cors, HorizonMin: cfg.Horizon, CountryOf: gip.Country,
			Backtest: func() (*backtest.Report, error) {
				return backtest.ReadReport(filepath.Join(filepath.Dir(dbPath), "backtest.json"))
			},
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr, "primary", cfg.Stations[0].ID)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			stop()
			<-ctx.Done()
			<-done
			return err
		}
	case <-ctx.Done():
	}
	violation := guard.Stopped(ctx, log)
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	<-done
	if violation != nil {
		return violation
	}
	return err
}
