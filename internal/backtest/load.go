package backtest

import (
	"context"
	"fmt"
	"time"

	"raincast/internal/geo"
	"raincast/internal/radar"
	"raincast/internal/rainviewer"
	"raincast/internal/store"
)

// DefaultConfig scores motion over 1–8 frame pairs and the intensity trend.
func DefaultConfig(step int) Config {
	cfg := Config{
		Leads: []int{10, 20, 30, 40, 50, 60}, Threshold: 20, Radius: 2, TrendTau: 20,
		Step: step, StepSec: 600,
	}
	for _, p := range []int{1, 2, 4, 6, 8} {
		cfg.Variants = append(cfg.Variants, Variant{Name: fmt.Sprintf("%d cặp", p), Pairs: p})
	}
	for _, p := range []int{2, 4} {
		cfg.Variants = append(cfg.Variants, Variant{Name: fmt.Sprintf("%d cặp + xu hướng", p), Pairs: p, Trend: true})
	}
	return cfg
}

// LoadFrames reads every frame listed in the database and stitches the 3×3
// tiles around (lat, lon) from the tile cache only: nothing is downloaded,
// and frames whose tiles were pruned are skipped and counted.
func LoadFrames(ctx context.Context, st *store.Store, cacheDir string, lat, lon float64) ([]Frame, int, error) {
	stored, err := st.RecentFrames(ctx, 5000)
	if err != nil {
		return nil, 0, err
	}
	client := rainviewer.New(cacheDir)
	client.Retries = 0
	const offline = "http://127.0.0.1:1" // a missing tile fails fast
	tx, ty := geo.TileOf(geo.LatLonToPixel(lat, lon, rainviewer.MaxZoom))
	var tiles []rainviewer.Tile
	for dy := range 3 {
		for dx := range 3 {
			tiles = append(tiles, rainviewer.Tile{Z: rainviewer.MaxZoom, X: tx - 1 + dx, Y: ty - 1 + dy})
		}
	}

	var frames []Frame
	skipped := 0
	for _, f := range stored {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		raw, err := client.FetchTiles(ctx, offline, f.Path, tiles)
		if err != nil {
			skipped++
			continue
		}
		byKey := map[radar.TileKey][]byte{}
		for t, d := range raw {
			byKey[radar.TileKey{X: t.X, Y: t.Y}] = d
		}
		m, err := radar.BuildMosaic(byKey, rainviewer.MaxZoom, tx-1, ty-1, 3, radar.DefaultPalette)
		if err != nil {
			skipped++
			continue
		}
		frames = append(frames, Frame{Time: f.Time.Unix(), Mosaic: m})
	}
	return frames, skipped, nil
}

// RunStored loads stored frames and scores the default variants.
func RunStored(ctx context.Context, st *store.Store, cacheDir string, lat, lon float64, step int) (Report, error) {
	start := time.Now()
	frames, skipped, err := LoadFrames(ctx, st, cacheDir, lat, lon)
	if err != nil {
		return Report{}, err
	}
	rep := Run(frames, DefaultConfig(step))
	rep.Frames, rep.Skipped = len(frames), skipped
	rep.GeneratedAt = time.Now().UTC()
	rep.Duration = time.Since(start).Round(time.Millisecond).String()
	return rep, nil
}
