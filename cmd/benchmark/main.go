// Command benchmark records RainCast's forecasts next to other providers'
// (Tomorrow.io, Rainbow.ai) every 10 minutes at a few stations, and the
// METAR of stations named by an airport's ICAO code, for the admin page to
// score each against METAR, as weatherindex.ai does, and against the
// others' later observations (package bench). It shares the database with cmd/collect, and needs the providers'
// API keys; a provider without one is left out.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"raincast/internal/bench"
	"raincast/internal/dotenv"
	"raincast/internal/env"
	"raincast/internal/guard"
	"raincast/internal/ml"
	"raincast/internal/mlmodel"
	"raincast/internal/pipeline"
	"raincast/internal/rainviewer"
	"raincast/internal/store"
)

func main() {
	if err := dotenv.Load(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "load .env:", err)
		os.Exit(1)
	}
	dbURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL, shared with collect (default $DATABASE_URL)")
	stationsFile := flag.String("stations", "", `JSON file of stations [{"id","name","lat","lon"}] to benchmark; an id that is an ICAO code (e.g. VTBS) is also scored against that airport's METAR (default: Bangkok Suvarnabhumi, which every provider covers; Rainbow.ai has no forecast for Vietnam); every station costs each provider a call per 10 minutes`)
	tomorrowKey := flag.String("tomorrow-key", os.Getenv("TOMORROW_API_KEY"), "Tomorrow.io API key (default $TOMORROW_API_KEY; empty: Tomorrow.io is left out)")
	rainbowToken := flag.String("rainbow-token", os.Getenv("RAINBOW_API_TOKEN"), "Rainbow.ai API token (default $RAINBOW_API_TOKEN; empty: Rainbow.ai is left out)")
	rainbowMonthly := flag.Int("rainbow-monthly", 4800, "Rainbow.ai calls per calendar month (UTC) before it is skipped; the free tier has 5,000")
	cacheDir := flag.String("cache", "data/tiles-bench", "radar tile cache directory for RainCast's forecasts")
	rateLimit := flag.Int("rate-limit", 30, "RainViewer requests per minute; RainViewer allows 100 per IP, shared with raincast and collect")
	useML := flag.Bool("ml", true, "post-process RainCast's forecasts with the embedded ML model, as the app does")
	debug := flag.Bool("debug", false, "verbose logging")
	limits := guard.Flags(flag.CommandLine, guard.DefaultLimits("data", "data/tiles-bench"))
	flag.Parse()

	log := env.Logger(env.Production(false), *debug)
	cfg := pipeline.DefaultConfig()
	// Rainbow.ai's nowcast does not cover Vietnam; Bangkok has the same
	// tropical monsoon rain and every provider. At its airport, METAR is
	// the ground truth weatherindex.ai scores against.
	cfg.Stations = []pipeline.Station{{ID: "VTBS", Name: "Bangkok Suvarnabhumi", Lat: 13.6900, Lon: 100.7501}}
	if *stationsFile != "" {
		st, err := pipeline.LoadStations(*stationsFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		cfg.Stations = st
	}
	// The newest frame is already minutes old when a forecast is asked
	// for: reach the hour scored from now, not from the frame.
	cfg.Horizon = int(bench.Horizon/time.Minute) + 30
	cfg.CacheAge = 3 * time.Hour
	if *useML {
		cfg.ML = sync.OnceValue(func() *ml.Bundle {
			b, err := mlmodel.Load()
			if err != nil {
				log.Error("load ML model", "err", err)
				return nil
			}
			return b
		})
	}

	lim := limits()
	lim.DirPath = *cacheDir
	if err := run(cfg, lim, *dbURL, *cacheDir, *rateLimit, *tomorrowKey, *rainbowToken, *rainbowMonthly, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfg pipeline.Config, lim guard.Limits, dbURL, cacheDir string, rateLimit int,
	tomorrowKey, rainbowToken string, rainbowMonthly int, log *slog.Logger) error {
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, _ := guard.Start(sigCtx, lim, log)
	lim.Log(log)

	st, err := store.Open(dbURL)
	if err != nil {
		return err
	}
	defer st.Close()

	client := rainviewer.New(cacheDir)
	client.SetRateLimit(rateLimit)
	r := &bench.Runner{
		Stations: cfg.Stations,
		RainCast: &bench.RainCast{P: pipeline.New(cfg, client, log)},
		METAR:    bench.NewMETAR(),
		Sink:     st, Log: log, Settle: 20 * time.Second,
	}
	if tomorrowKey != "" {
		r.Providers = append(r.Providers, bench.NewTomorrow(tomorrowKey))
	} else {
		log.Warn("TOMORROW_API_KEY is not set: Tomorrow.io is left out")
	}
	if rainbowToken != "" {
		r.Providers = append(r.Providers, &bench.MonthlyCap{
			Provider: bench.NewRainbow(rainbowToken), Limit: rainbowMonthly,
			Count: func(ctx context.Context, since time.Time) (int, error) {
				return st.BenchCalls(ctx, bench.NameRainbow, since)
			},
		})
	} else {
		log.Warn("RAINBOW_API_TOKEN is not set: Rainbow.ai is left out")
	}
	log.Info("benchmarking", "stations", len(cfg.Stations), "providers", len(r.Providers), "every", bench.Step)

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx)
	}()
	// Pipeline.Tick, run before each forecast, prunes the tile cache.

	<-ctx.Done()
	violation := guard.Stopped(ctx, log)
	log.Info("shutting down")
	<-done
	if violation != nil {
		return violation
	}
	return nil
}
