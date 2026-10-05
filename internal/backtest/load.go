package backtest

import (
	"context"
	"fmt"
	"time"

	"raincast/internal/radar"
	"raincast/internal/rainviewer"
	"raincast/internal/store"
)

// methodNames label the methods in reports and the CLI.
var methodNames = map[string]string{
	MethodTREC: "TREC", MethodHS: "Horn–Schunck", MethodLK: "Lucas–Kanade", MethodMean: "Ensemble TB",
}

// motionPairs is how many frame pairs every method averages, the
// pipeline's setting.
const motionPairs = 4

// DefaultConfig scores every method at 4 pairs, plain and + trend, and the
// equal mean of the three both ways (model.Default is the mean + trend).
// TREC is the reference.
func DefaultConfig(step int) Config {
	cfg := Config{
		Leads: []int{10, 20, 30, 40, 50, 60}, Threshold: 20, Classes: DefaultClasses(), Radius: 2, TrendTau: 20,
		FSSThresholds: []float32{20, 30, 40}, FSSWindows: []int{1, 3, 5},
		Step: step, StepSec: 600, EventRain: 0.02, EventFrames: 6,
	}
	name := func(method string, trend bool) string {
		n := methodNames[method]
		if method == MethodTREC {
			n = fmt.Sprintf("TREC %d cặp", motionPairs)
		}
		if trend {
			n += " + xu hướng"
		}
		return n
	}
	cfg.Reference = name(MethodTREC, false)
	var plain, trended []string
	for _, m := range []string{MethodTREC, MethodHS, MethodLK} {
		plain = append(plain, name(m, false))
		trended = append(trended, name(m, true))
		cfg.Variants = append(cfg.Variants,
			Variant{Name: name(m, false), Method: m, Pairs: motionPairs},
			Variant{Name: name(m, true), Method: m, Pairs: motionPairs, Trend: true})
	}
	cfg.Variants = append(cfg.Variants,
		Variant{Name: name(MethodMean, false), Method: MethodMean, Members: plain},
		Variant{Name: name(MethodMean, true), Method: MethodMean, Members: trended, Trend: true},
	)
	return cfg
}

// PaperConfig scores the way published nowcast verifications do, so CSIs
// compare with theirs: every sample is its own pixel (~1.2 km) rather than
// the median of a disc around it, which smooths both sides; leads run to 90
// minutes; and the classes are their rain-rate and reflectivity thresholds.
func PaperConfig(step int) Config {
	cfg := DefaultConfig(step)
	cfg.Radius = 0
	cfg.Leads = []int{10, 20, 30, 40, 50, 60, 70, 80, 90}
	cfg.Classes = PaperClasses()
	return cfg
}

// tileSource reads one region's mosaics from the tile cache only: nothing
// is downloaded, and frames whose tiles are missing count as skipped.
type tileSource struct {
	client *rainviewer.Client
	times  []int64
	paths  map[int64]string
	tx, ty int
}

func (s *tileSource) Times() []int64 { return s.times }

func (s *tileSource) Load(t int64) *radar.Mosaic {
	path, ok := s.paths[t]
	if !ok {
		return nil
	}
	byKey := map[radar.TileKey][]byte{}
	for dy := range 3 {
		for dx := range 3 {
			x, y := s.tx-1+dx, s.ty-1+dy
			data, ok := s.client.CachedTile(path, rainviewer.Tile{Z: rainviewer.MaxZoom, X: x, Y: y})
			if !ok {
				return nil
			}
			byKey[radar.TileKey{X: x, Y: y}] = data
		}
	}
	m, err := radar.BuildMosaic(byKey, rainviewer.MaxZoom, s.tx-1, s.ty-1, 3, radar.DefaultPalette)
	if err != nil {
		return nil
	}
	return m
}

// coverage stitches the cached coverage of the mosaic around (tx, ty), or
// returns nil when any tile was never fetched.
func coverage(client *rainviewer.Client, tx, ty int) *radar.Coverage {
	byKey := map[radar.TileKey][]byte{}
	for dy := range 3 {
		for dx := range 3 {
			x, y := tx-1+dx, ty-1+dy
			data, ok := client.CachedCoverage(rainviewer.Tile{Z: rainviewer.MaxZoom, X: x, Y: y})
			if !ok {
				return nil
			}
			byKey[radar.TileKey{X: x, Y: y}] = data
		}
	}
	cov, err := radar.BuildCoverageMosaic(byKey, tx-1, ty-1, 3)
	if err != nil {
		return nil
	}
	return cov
}

// backfill is how far before activation the collector may have cached
// frames (it backfills the feed's past two hours).
const backfill = 3 * time.Hour

// Options control a stored run.
type Options struct {
	Step int // sample spacing in pixels
	Days int // frames from the last Days days; all when <= 0
	// Parallel and Workers bound CPU use (see Config).
	Parallel, Workers int
	// StateFile keeps scores between runs, so each run only scores new
	// forecast times; empty scores everything afresh.
	StateFile string
	// Keep drops saved scores older than this (0 keeps all).
	Keep time.Duration
	// Config replaces DefaultConfig(Step) when set, e.g. PaperConfig.
	Config *Config
}

// RunStored scores every collected region over the recorded frames.
func RunStored(ctx context.Context, st *store.Store, cacheDir string, o Options) (Report, error) {
	start := time.Now()
	var from int64
	if o.Days > 0 {
		from = time.Now().Add(-time.Duration(o.Days) * 24 * time.Hour).Unix()
	}
	frames, err := st.FrameList(ctx, from)
	if err != nil {
		return Report{}, err
	}
	collected, err := st.Regions(ctx)
	if err != nil {
		return Report{}, err
	}
	client := rainviewer.New(cacheDir)
	paths := make(map[int64]string, len(frames))
	all := make([]int64, 0, len(frames))
	for _, f := range frames {
		paths[f.Time] = f.Path
		all = append(all, f.Time)
	}
	between := func(lo, hi int64) []int64 {
		var out []int64
		for _, t := range all {
			if t >= lo && t <= hi {
				out = append(out, t)
			}
		}
		return out
	}

	var regions []Region
	for _, r := range collected {
		regions = append(regions, Region{
			Name: fmt.Sprintf("%.1f, %.1f", r.Lat, r.Lon), Climate: r.Climate,
			Source: &tileSource{client: client, times: between(r.FirstSeen-int64(backfill.Seconds()), r.LastActive),
				paths: paths, tx: r.TileX, ty: r.TileY},
			Coverage: coverage(client, r.TileX, r.TileY)})
	}

	cfg := DefaultConfig(o.Step)
	if o.Config != nil {
		cfg = *o.Config
	}
	cfg.Parallel, cfg.Workers = o.Parallel, o.Workers
	var state *State
	if o.StateFile != "" {
		state = LoadState(o.StateFile)
	}
	if state == nil || state.Version != cfg.version() {
		state = NewState(cfg)
	}
	if o.Keep > 0 {
		state.Prune(time.Now().Add(-o.Keep).Unix())
	}
	rep, err := RunRegions(ctx, regions, cfg, state)
	if err != nil {
		return Report{}, err
	}
	if o.StateFile != "" {
		if err := state.Save(o.StateFile); err != nil {
			return Report{}, err
		}
	}
	rep.GeneratedAt = time.Now().UTC()
	rep.Duration = time.Since(start).Round(time.Millisecond).String()
	return rep, nil
}
