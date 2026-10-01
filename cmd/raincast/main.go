// Command raincast predicts when rain will reach a location from RainViewer
// radar frames and serves the forecast and its track record over HTTP.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"raincast/internal/backtest"
	"raincast/internal/geocode"
	"raincast/internal/pipeline"
	"raincast/internal/rainviewer"
	"raincast/internal/server"
	"raincast/internal/store"
)

func main() {
	// Secrets such as LOCATIONIQ_KEY come from the environment or .env.
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
	flag.IntVar(&cfg.MotionPairs, "motion-pairs", cfg.MotionPairs, "frame pairs (10 min each) averaged to estimate rain motion")
	flag.BoolVar(&cfg.UseTrend, "trend", cfg.UseTrend, "serve forecasts with intensity growth/decay (both versions are always recorded)")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "data/raincast.db", "SQLite database path")
	cacheDir := flag.String("cache", "data/tiles", "tile cache directory")
	poll := flag.Duration("poll", 2*time.Minute, "how often to check for new frames")
	cors := flag.String("cors", "http://localhost:3000", "allowed CORS origin (empty to disable)")
	debug := flag.Bool("debug", false, "verbose logging")
	geoUA := flag.String("geocode-ua", "raincast/1.0", "User-Agent sent to Nominatim (policy: identify your app, ideally with contact info)")
	geoCountries := flag.String("geocode-countries", "vn", "comma-separated country codes to limit address search (empty for worldwide)")
	flag.Parse()
	cfg.Threshold = float32(threshold)
	cfg.Heavy = float32(heavy)
	cfg.Likely = float32(likely)
	if cfg.MotionPairs < 1 {
		fmt.Fprintln(os.Stderr, "-motion-pairs must be at least 1")
		os.Exit(2)
	}
	if *stationsFile != "" {
		st, err := loadStations(*stationsFile)
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

	geo := geocode.New(*geoUA, *geoCountries)
	geo.Bias = &geocode.LatLon{Lat: cfg.Stations[0].Lat, Lon: cfg.Stations[0].Lon}
	// LocationIQ makes suggestions fast; without a key Photon is used.
	geo.LocationIQKey = os.Getenv("LOCATIONIQ_KEY")
	geo.Log = log
	log.Info("config", "stations", len(cfg.Stations), "locationiq", geo.LocationIQKey != "")
	if err := run(cfg, geo, *addr, *dbPath, *cacheDir, *poll, *cors, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfg pipeline.Config, geo *geocode.Client, addr, dbPath, cacheDir string, poll time.Duration, cors string, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(dbPath, float64(cfg.Threshold))
	if err != nil {
		return err
	}
	defer st.Close()

	p := pipeline.New(cfg, rainviewer.New(cacheDir), st, log)
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(ctx, poll)
	}()

	srv := &http.Server{
		Addr: addr,
		Handler: server.New(p, geo, st, log, server.Config{
			CORSOrigin: cors, HorizonMin: cfg.Horizon,
			BacktestFile: filepath.Join(filepath.Dir(dbPath), "backtest.json"),
			Backtest: func(ctx context.Context) (backtest.Report, error) {
				pr := cfg.Stations[0]
				return backtest.RunStored(ctx, st, cacheDir, pr.Lat, pr.Lon, 8)
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
			<-done
			return err
		}
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	<-done
	return err
}

// loadStations reads and validates a stations file.
func loadStations(path string) ([]pipeline.Station, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st []pipeline.Station
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(st) == 0 {
		return nil, fmt.Errorf("%s: no stations", path)
	}
	seen := map[string]bool{}
	for _, s := range st {
		if s.ID == "" || seen[s.ID] {
			return nil, fmt.Errorf("%s: station ids must be unique and non-empty (%q)", path, s.ID)
		}
		if s.Lat < -85 || s.Lat > 85 || s.Lon < -180 || s.Lon > 180 {
			return nil, fmt.Errorf("%s: station %q has invalid coordinates", path, s.ID)
		}
		seen[s.ID] = true
	}
	return st, nil
}
