// Command collect stores radar frames for backtesting, on its own: the
// rainiest radar-covered regions worldwide, every region alike. It keeps
// the machine awake and stops before overloading it. It shares the
// database with raincast and cmd/backtest, and the tile cache with
// cmd/backtest only.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"raincast/internal/collect"
	"raincast/internal/dotenv"
	"raincast/internal/env"
	"raincast/internal/guard"
	"raincast/internal/himawari"
	"raincast/internal/pipeline"
	"raincast/internal/rainviewer"
	"raincast/internal/store"
)

func main() {
	// DATABASE_URL comes from the environment or .env.
	if err := dotenv.Load(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "load .env:", err)
		os.Exit(1)
	}
	dbURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL for the collected frames, shared with backtest (default $DATABASE_URL)")
	dataDir := flag.String("data", "data", "directory whose disk the guard watches")
	cacheDir := flag.String("cache", "data/tiles", "tile cache directory, read by cmd/backtest")
	poll := flag.Duration("poll", 2*time.Minute, "how often to check for new frames")
	cacheAge := flag.Duration("cache-age", pipeline.DefaultConfig().CacheAge, "how long radar tiles are kept for backtesting")
	rateLimit := flag.Int("rate-limit", 90, "RainViewer requests per minute for this process; RainViewer allows 100 per IP, so lower this when raincast shares the IP")
	satCache := flag.String("sat-cache", "", "Himawari tile cache, read by cmd/backtest (e.g. data/himawari; keep it apart from -cache, which is pruned on its own); empty, the default, collects no satellite: it added under a CSI point to the ML")
	satRate := flag.Float64("sat-rate", 1, "JMA requests per second")
	col := collect.DefaultConfig()
	flag.IntVar(&col.Regions, "regions", col.Regions, "rainy radar regions worldwide to collect")
	keepAwake := flag.Bool("keep-awake", true, "stop Windows from sleeping on its own while running, so collection has no gaps")
	limits := guard.Flags(flag.CommandLine, guard.DefaultLimits("data", "data/tiles"))
	debug := flag.Bool("debug", false, "verbose logging")
	flag.Parse()

	log := env.Logger(env.Production(false), *debug)
	lim := limits()
	lim.DiskPath, lim.DirPath = *dataDir, *cacheDir
	if err := run(col, lim, *keepAwake, *dbURL, *cacheDir, *satCache, *satRate, *poll, *cacheAge, *rateLimit, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(col collect.Config, lim guard.Limits, keepAwake bool,
	dbURL, cacheDir, satCache string, satRate float64, poll, cacheAge time.Duration, rateLimit int, log *slog.Logger) error {
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Everything long-running derives from the guard's context, so crossing
	// a resource limit stops all of it.
	ctx, _ := guard.Start(sigCtx, lim, log)
	lim.Log(log)
	if keepAwake {
		go guard.KeepAwake(ctx)
	}

	st, err := store.Open(dbURL)
	if err != nil {
		return err
	}
	defer st.Close()

	client := rainviewer.New(cacheDir)
	client.SetRateLimit(rateLimit)
	feed := collect.NewPoller(client, log)
	go feed.Run(ctx, poll)
	c := collect.New(col, client, st, feed.Feed, log)
	var sat *himawari.Client
	if satCache != "" {
		sat = himawari.New(satCache, satRate)
		c.Sat = sat
	}
	log.Info("collecting", "rainy_regions", col.Regions, "rate_limit", rateLimit, "satellite", satCache != "")

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx, poll)
	}()
	// Tiles older than cacheAge are of no use to the backtest any more.
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if err := client.PruneCache(cacheAge); err != nil {
				log.Warn("prune cache", "err", err)
			}
			if sat != nil {
				if err := sat.PruneCache(cacheAge); err != nil {
					log.Warn("prune satellite cache", "err", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	<-ctx.Done()
	violation := guard.Stopped(ctx, log)
	log.Info("shutting down")
	<-done
	if violation != nil {
		return violation
	}
	return nil
}
