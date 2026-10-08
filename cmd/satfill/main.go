// Command satfill caches the Himawari tiles of radar frames already
// collected, for the regions the satellite sees, so the backtest has cloud
// tops for them. JMA keeps tiles only a few days, so it goes oldest first;
// tiles already cached cost nothing, so it can be stopped and rerun.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"raincast/internal/dotenv"
	"raincast/internal/env"
	"raincast/internal/himawari"
	"raincast/internal/progress"
	"raincast/internal/store"
)

func main() {
	if err := dotenv.Load(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "load .env:", err)
		os.Exit(1)
	}
	dbURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL of the collected frames (default $DATABASE_URL)")
	satCache := flag.String("sat-cache", "data/himawari", "Himawari tile cache, read by cmd/backtest")
	days := flag.Int("days", 6, "frames from the last days only; JMA keeps tiles about 5 days")
	perSecond := flag.Float64("rate", 2, "JMA requests per second")
	margin := flag.Int("margin", himawari.DefaultMargin, "satellite pixels (~5 km) kept around each radar mosaic")
	dryRun := flag.Bool("dry-run", false, "only count the tiles needed")
	flag.Parse()
	log := env.Logger(false, false)
	if err := run(*dbURL, *satCache, *days, *perSecond, *margin, *dryRun, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

type job struct {
	t     int64 // radar frame
	tiles []himawari.Tile
}

func run(dbURL, satCache string, days int, perSecond float64, margin int, dryRun bool, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(dbURL)
	if err != nil {
		return err
	}
	defer st.Close()
	regions, err := st.Regions(ctx)
	if err != nil {
		return err
	}
	from := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	frames, err := st.FrameList(ctx, from)
	if err != nil {
		return err
	}

	// Each frame needs the tiles of every seen region active around it
	// (with the history and future the backtest reads), merged.
	const before, after = 3 * 3600, 2 * 3600
	byTime := map[int64]map[himawari.Tile]bool{}
	seen := 0
	for _, r := range regions {
		if !himawari.Covers(r.Lat, r.Lon) {
			continue
		}
		seen++
		tiles := himawari.RegionTiles(r.TileX, r.TileY, margin)
		for _, f := range frames {
			if f.Time < r.FirstSeen-before || f.Time > r.LastActive+after {
				continue
			}
			set := byTime[f.Time]
			if set == nil {
				set = map[himawari.Tile]bool{}
				byTime[f.Time] = set
			}
			for _, t := range tiles {
				set[t] = true
			}
		}
	}
	jobs := make([]job, 0, len(byTime))
	total := 0
	for t, set := range byTime {
		j := job{t: t}
		for tl := range set {
			j.tiles = append(j.tiles, tl)
		}
		total += len(j.tiles)
		jobs = append(jobs, j)
	}
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].t < jobs[b].t })
	log.Info("satfill", "regions", len(regions), "seen_by_himawari", seen, "frames", len(jobs), "tiles", total,
		"hours_at_rate", fmt.Sprintf("%.1f", float64(total)/perSecond/3600))
	if dryRun || len(jobs) == 0 {
		return nil
	}

	c := himawari.New(satCache, perSecond)
	var got, none int
	start := time.Now()
	for i, j := range jobs {
		for {
			scan, err := c.PrefetchFor(ctx, j.t, j.tiles)
			if err == nil {
				if scan == 0 {
					none++
				} else {
					got++
				}
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "cooling down") {
				log.Warn("satfill: frame skipped", "time", time.Unix(j.t, 0).UTC(), "err", err)
				break
			}
			// Throttled: wait the cool-down out, then go on.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(30 * time.Second):
			}
		}
		if (i+1)%100 == 0 || i == len(jobs)-1 {
			s := c.Stats()
			args := progress.New(int64(i+1), int64(len(jobs)), time.Since(start)).Args()
			args = append(args, "with_scan", got, "no_scan", none, "requests", s.Requests, "cached", s.CacheHits,
				"missing", s.Missing, "throttled", s.Throttled)
			log.Info("satfill: progress", args...)
		}
	}
	return nil
}
