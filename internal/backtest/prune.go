package backtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"raincast/internal/rainviewer"
	"raincast/internal/store"
)

// PruneKeep is how recent data is kept whatever its use: the collector may
// still extend a region's stretch, and refetches tiles of frames its feed
// still lists when they go missing.
const PruneKeep = 3 * time.Hour

// frameDirPrefix starts the cache directory of every radar frame
// (rainviewer.Client.FrameDir of a /v2/radar/… path).
const frameDirPrefix = "v2_radar_"

// PrunePlan is what no backtest can use: frames, tiles and regions outside
// every usable forecast's window (its history and leads).
type PrunePlan struct {
	Frames  []int64        // recorded frames no region needs
	Regions []store.Region // regions with no usable forecast time
	Dirs    []string       // frame directories to remove whole
	Files   []string       // tile files to remove from kept frames
	Bytes   int64          // freed on disk
}

// PlanPrune works out, without deleting anything, what PrunePlan.Apply
// would remove; nothing newer than now − PruneKeep is touched.
func PlanPrune(ctx context.Context, st *store.Store, cacheDir string, cfg Config, now time.Time) (*PrunePlan, error) {
	inv, err := takeInventory(ctx, st, cacheDir, cfg)
	if err != nil {
		return nil, err
	}
	cut := now.Add(-PruneKeep).Unix()
	lead := int64(cfg.Leads[len(cfg.Leads)-1]) * 60
	back := int64(inv.pairs) * cfg.StepSec

	// The tiles each frame must keep.
	need := map[int64]map[string]bool{}
	keep := func(t int64, r store.Region) {
		if need[t] == nil {
			need[t] = map[string]bool{}
		}
		for _, tile := range mosaicTiles(r.TileX, r.TileY) {
			need[t][rainviewer.TileFile(tile)] = true
		}
	}
	plan := &PrunePlan{}
	for _, ri := range inv.regions {
		for _, t := range ri.issues {
			for ft := t - back; ft <= t+lead; ft += cfg.StepSec {
				keep(ft, ri.region)
			}
		}
		active := ri.region.LastActive >= cut
		if n := len(ri.Runs); active && n > 0 {
			// The stretch being collected may still become usable.
			for ft := ri.Runs[n-1].From; ft <= ri.Runs[n-1].To; ft += cfg.StepSec {
				keep(ft, ri.region)
			}
		}
		if ri.Usable == 0 && !active {
			plan.Regions = append(plan.Regions, ri.region)
		}
	}

	known := map[string]bool{}
	for _, f := range inv.frames {
		dir := inv.client.FrameDir(f.Path)
		known[filepath.Base(dir)] = true
		if f.Time >= cut {
			continue
		}
		if need[f.Time] == nil {
			plan.Frames = append(plan.Frames, f.Time)
			plan.Dirs = append(plan.Dirs, dir)
			plan.Bytes += dirSize(dir)
			continue
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() || need[f.Time][e.Name()] {
				continue
			}
			plan.Files = append(plan.Files, filepath.Join(dir, e.Name()))
			if info, err := e.Info(); err == nil {
				plan.Bytes += info.Size()
			}
		}
	}
	// Directories of frames no longer recorded at all; only those named
	// like frames, so a wrong cacheDir deletes nothing else.
	entries, _ := os.ReadDir(cacheDir)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !e.IsDir() || !strings.HasPrefix(e.Name(), frameDirPrefix) || known[e.Name()] ||
			info.ModTime().Unix() >= cut {
			continue
		}
		dir := filepath.Join(cacheDir, e.Name())
		plan.Dirs = append(plan.Dirs, dir)
		plan.Bytes += dirSize(dir)
	}
	return plan, nil
}

// Apply deletes what the plan lists.
func (p *PrunePlan) Apply(ctx context.Context, st *store.Store) error {
	for _, f := range p.Files {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	for _, d := range p.Dirs {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
	}
	if err := st.DeleteFrames(ctx, p.Frames); err != nil {
		return err
	}
	for _, r := range p.Regions {
		if err := st.DeleteRegion(ctx, r.TileX, r.TileY); err != nil {
			return err
		}
	}
	return nil
}

func dirSize(dir string) int64 {
	var n int64
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() {
			n += info.Size()
		}
	}
	return n
}
