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

// A tile block keeps its motion cache across frames; other blocks do not
// share it.
func TestMotionCacheSharedAcrossFrames(t *testing.T) {
	p := New(DefaultConfig(), nil, nil)
	a := p.motionFor(100, 60, 1200, 600)
	if b := p.motionFor(100, 60, 1800, 1200); b != a {
		t.Error("the next frame got a new cache for the same tiles")
	}
	if c := p.motionFor(101, 60, 1800, 1200); c == a {
		t.Error("two tile blocks share a cache")
	}
}

// Caches not used since before the oldest frame read are dropped, and the
// least recently used go first beyond the cap.
func TestMotionCacheDropsStale(t *testing.T) {
	p := New(DefaultConfig(), nil, nil)
	p.motionFor(1, 1, 600, 0)
	p.motionFor(2, 2, 3000, 1200)
	if _, ok := p.motion[tileKey{1, 1}]; ok {
		t.Error("cache last used at 600 kept although forecasts now start at 1200")
	}
	for i := range maxMotionCaches + 5 {
		p.motionFor(10+i, 0, 3000+int64(i), 0)
	}
	if len(p.motion) != maxMotionCaches {
		t.Fatalf("%d caches, want %d", len(p.motion), maxMotionCaches)
	}
	if _, ok := p.motion[tileKey{2, 2}]; ok {
		t.Error("the least recently used cache survived the cap")
	}
	if _, ok := p.motion[tileKey{10 + maxMotionCaches + 4, 0}]; !ok {
		t.Error("the newest cache was dropped")
	}
}

// A full region cache makes room for one more instead of emptying.
func TestPutRegionKeepsOthersWhenFull(t *testing.T) {
	p := New(DefaultConfig(), nil, nil)
	p.putRegion(regionKey{0, 0, 600}, &region{})
	for i := range maxRegions + 1 {
		p.putRegion(regionKey{i, 0, 1200}, &region{})
	}
	if len(p.regions) != maxRegions {
		t.Fatalf("%d regions, want %d", len(p.regions), maxRegions)
	}
	if _, ok := p.regions[regionKey{maxRegions, 0, 1200}]; !ok {
		t.Error("the region just added is missing")
	}
	for k := range p.regions {
		if k.Frame != 1200 {
			t.Errorf("region %+v from an older frame kept", k)
		}
	}
}
