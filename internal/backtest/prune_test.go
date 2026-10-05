package backtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"raincast/internal/rainviewer"
	"raincast/internal/store"
)

// One region has two hours of frames in a row, usable; another, sharing a
// column of tiles with it, only its last three, too few for any forecast.
func TestPrunePlan(t *testing.T) {
	st, err := store.Open(store.TestDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	cache := t.TempDir()
	client := rainviewer.New(cache)
	cfg := DefaultConfig(8)
	const t0 = 1_700_000_400
	now := time.Unix(t0, 0).Add(24 * time.Hour)
	path := func(i int) string { return fmt.Sprintf("/v2/radar/f%02d", i) }
	write := func(i, tx, ty int) {
		for _, tile := range mosaicTiles(tx, ty) {
			p := filepath.Join(client.FrameDir(path(i)), rainviewer.TileFile(tile))
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte("png"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	const n = 13
	for i := range n {
		if err := st.RecordFrame(ctx, t0+int64(i)*600, path(i)); err != nil {
			t.Fatal(err)
		}
		write(i, 100, 50)
		if i >= n-3 {
			write(i, 102, 50)
		}
	}
	for _, r := range []store.Region{{TileX: 100, TileY: 50, Climate: "midlat"}, {TileX: 102, TileY: 50, Climate: "midlat"}} {
		if err := st.TouchRegion(ctx, r, t0); err != nil {
			t.Fatal(err)
		}
		if err := st.TouchRegion(ctx, r, t0+(n-1)*600); err != nil {
			t.Fatal(err)
		}
	}
	// A frame no longer recorded, and something else in the cache dir.
	orphan := filepath.Join(cache, "v2_radar_gone")
	other := filepath.Join(cache, "notes")
	for _, d := range []string{orphan, other} {
		os.MkdirAll(d, 0o755)
		os.Chtimes(d, time.Unix(t0, 0), time.Unix(t0, 0))
	}

	plan, err := PlanPrune(ctx, st, cache, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Regions) != 1 || plan.Regions[0].TileX != 102 {
		t.Errorf("regions to delete: %+v, want only the short one", plan.Regions)
	}
	if len(plan.Frames) != 0 {
		t.Errorf("frames to delete: %v, want none (all are in a usable window)", plan.Frames)
	}
	// The short region's two columns of its own, in its three frames; the
	// column it shares stays.
	if len(plan.Files) != 6*3 {
		t.Errorf("%d tile files to delete, want 18", len(plan.Files))
	}
	if !slices.Contains(plan.Dirs, orphan) || slices.Contains(plan.Dirs, other) {
		t.Errorf("dirs to delete: %v", plan.Dirs)
	}
	if err := plan.Apply(ctx, st); err != nil {
		t.Fatal(err)
	}
	inv, err := Inventory(ctx, st, cache, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv) != 1 || inv[0].Usable != 3 || inv[0].Cached != n {
		t.Errorf("after pruning: %+v", inv)
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("pruning removed a directory that is not a frame's")
	}

	// Nothing recent is touched.
	plan, err = PlanPrune(ctx, st, cache, cfg, time.Unix(t0, 0).Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Frames)+len(plan.Files)+len(plan.Dirs)+len(plan.Regions) != 0 {
		t.Errorf("recent data planned for deletion: %+v", plan)
	}
}
