package pipeline

import (
	"testing"

	"raincast/internal/model"
	"raincast/internal/radar"
	"raincast/internal/rainviewer"
)

// Frames further apart than MaxGap are not paired for motion.
func TestBuilderPairsOnlyCloseFrames(t *testing.T) {
	p := &Pipeline{cfg: DefaultConfig()}
	history := []rainviewer.Frame{{Time: 600}, {Time: 1200}, {Time: 4800}, {Time: 5400}} // 60-min gap
	grid := func(int64) *radar.Mosaic { return &radar.Mosaic{Grid: radar.NewGrid(4, 4)} }
	b := p.builder(history, grid, model.NewCache())
	if prev, ok := b.Prev(5400); !ok || prev != 4800 {
		t.Errorf("Prev(5400) = %d, %v; want 4800", prev, ok)
	}
	if _, ok := b.Prev(4800); ok {
		t.Error("frames an hour apart were paired")
	}
	if _, ok := b.Prev(600); ok {
		t.Error("the first frame has no earlier one")
	}
	if b.Grid(1200) == nil {
		t.Error("grid not passed through")
	}
}

func TestDefaultConfigUsesDefaultModel(t *testing.T) {
	m := DefaultConfig().Model
	if m.Name != model.Default().Name || !m.Trend || len(m.Members) != 3 {
		t.Fatalf("default model %+v", m)
	}
}

// Every point ends up at least half a tile inside its 2×2 region.
func TestRegionCornerKeepsMargin(t *testing.T) {
	for _, gx := range []float64{256*101 + 1, 256*101 + 127.9, 256*101 + 128, 256*101 + 255} {
		x0, _ := regionCorner(gx, gx)
		local := gx - float64(x0*256)
		if local < 128 || local > 2*256-128 {
			t.Errorf("gx %.1f: corner tile %d puts it %.1f px in, want within [128, 384]", gx, x0, local)
		}
	}
	if a, _ := regionCorner(256*101+10, 0); a != 100 {
		t.Errorf("left half of tile 101 should start at tile 100, got %d", a)
	}
	if a, _ := regionCorner(256*101+200, 0); a != 101 {
		t.Errorf("right half of tile 101 should start at tile 101, got %d", a)
	}
}
