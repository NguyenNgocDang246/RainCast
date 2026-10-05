package backtest

import (
	"context"
	"fmt"

	"raincast/internal/rainviewer"
	"raincast/internal/store"
)

// Stretch is a run of consecutive frames, every one with its tiles cached.
type Stretch struct {
	From, To int64 // unix s, first and last frame
	Frames   int
}

// RegionInventory is what the tile cache holds for one region.
type RegionInventory struct {
	Name    string
	Climate string
	// Expected counts the frames recorded while the region was collected,
	// Cached those with all its tiles on disk.
	Expected, Cached int
	Runs             []Stretch
	// Usable counts the cached frames a forecast can be issued and scored
	// from: enough history for the motion and a frame at every lead.
	Usable int

	region store.Region
	have   map[int64]bool // cached frames
	issues []int64        // usable frames
}

// inventory is every region's inventory over the recorded frames.
type inventory struct {
	frames  []store.FrameRef
	regions []RegionInventory
	client  *rainviewer.Client
	pairs   int // history steps an issue needs
}

// Inventory checks, without scoring anything, which frames each region
// has in the tile cache, how they run together, and how many the backtest
// can issue forecasts from with cfg.
func Inventory(ctx context.Context, st *store.Store, cacheDir string, cfg Config) ([]RegionInventory, error) {
	inv, err := takeInventory(ctx, st, cacheDir, cfg)
	if err != nil {
		return nil, err
	}
	return inv.regions, nil
}

func takeInventory(ctx context.Context, st *store.Store, cacheDir string, cfg Config) (*inventory, error) {
	frames, err := st.FrameList(ctx, 0)
	if err != nil {
		return nil, err
	}
	regions, err := st.Regions(ctx)
	if err != nil {
		return nil, err
	}
	inv := &inventory{frames: frames, client: rainviewer.New(cacheDir), pairs: 1}
	for _, v := range cfg.Variants {
		inv.pairs = max(inv.pairs, v.Pairs)
	}
	for _, r := range regions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ri := RegionInventory{Name: fmt.Sprintf("%.1f, %.1f", r.Lat, r.Lon), Climate: r.Climate,
			region: r, have: map[int64]bool{}}
		lo := r.FirstSeen - int64(backfill.Seconds())
		for _, f := range frames {
			if f.Time < lo || f.Time > r.LastActive {
				continue
			}
			ri.Expected++
			if !cachedMosaic(inv.client, f.Path, r.TileX, r.TileY) {
				continue
			}
			ri.have[f.Time] = true
			ri.Cached++
			if n := len(ri.Runs); n > 0 && ri.Runs[n-1].To == f.Time-cfg.StepSec {
				ri.Runs[n-1].To = f.Time
				ri.Runs[n-1].Frames++
			} else {
				ri.Runs = append(ri.Runs, Stretch{From: f.Time, To: f.Time, Frames: 1})
			}
		}
		for _, f := range frames {
			if ri.have[f.Time] && usable(ri.have, f.Time, inv.pairs, cfg) {
				ri.issues = append(ri.issues, f.Time)
			}
		}
		ri.Usable = len(ri.issues)
		inv.regions = append(inv.regions, ri)
	}
	return inv, nil
}

// mosaicTiles are the 3×3 tiles around (tx, ty).
func mosaicTiles(tx, ty int) []rainviewer.Tile {
	out := make([]rainviewer.Tile, 0, 9)
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			out = append(out, rainviewer.Tile{Z: rainviewer.MaxZoom, X: tx + dx, Y: ty + dy})
		}
	}
	return out
}

// cachedMosaic reports whether all 3×3 tiles around (tx, ty) are cached.
func cachedMosaic(c *rainviewer.Client, path string, tx, ty int) bool {
	for _, t := range mosaicTiles(tx, ty) {
		if !c.HasCachedTile(path, t) {
			return false
		}
	}
	return true
}

// usable mirrors scoreRegion: pairs steps of history and every lead.
func usable(have map[int64]bool, t int64, pairs int, cfg Config) bool {
	for k := -pairs; k <= 0; k++ {
		if !have[t+int64(k)*cfg.StepSec] {
			return false
		}
	}
	for _, l := range cfg.Leads {
		if !have[t+int64(l)*60] {
			return false
		}
	}
	return true
}
