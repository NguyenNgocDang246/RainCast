package collect

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"raincast/internal/radar"
	"raincast/internal/rainviewer"
	"raincast/internal/store"
)

// rect is a pixel rectangle inside one tile.
type rect struct{ x0, y0, x1, y1 int }

// fakeWorld serves synthetic radar and coverage tiles.
type fakeWorld struct {
	mu sync.Mutex
	// rain maps a tile to rainy rectangles (40 dBZ) per frame path; "*"
	// applies to every frame.
	rain map[rainviewer.Tile]map[string][]rect
	// covered lists covered tiles; others are black (no radar).
	covered  map[rainviewer.Tile]bool
	fetched  map[string]int
	coverage int
	pngs     map[string][]byte // encoded tiles by content
}

func newWorld() *fakeWorld {
	return &fakeWorld{rain: map[rainviewer.Tile]map[string][]rect{}, covered: map[rainviewer.Tile]bool{}, fetched: map[string]int{}, pngs: map[string][]byte{}}
}

func (w *fakeWorld) addRain(t rainviewer.Tile, path string, r rect) {
	if w.rain[t] == nil {
		w.rain[t] = map[string][]rect{}
	}
	w.rain[t][path] = append(w.rain[t][path], r)
}

func encode(img image.Image) []byte {
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func (w *fakeWorld) FetchTile(_ context.Context, _, path string, t rainviewer.Tile) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fetched[fmt.Sprint(path, t)]++
	rs := append(append([]rect(nil), w.rain[t]["*"]...), w.rain[t][path]...)
	id := fmt.Sprint(rs)
	if d, ok := w.pngs[id]; ok {
		return d, nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	c := radar.DefaultPalette.Color(40)
	for _, r := range rs {
		for y := r.y0; y < r.y1; y++ {
			for x := r.x0; x < r.x1; x++ {
				img.SetNRGBA(x, y, c)
			}
		}
	}
	w.pngs[id] = encode(img)
	return w.pngs[id], nil
}

func (w *fakeWorld) FetchTiles(ctx context.Context, host, path string, tiles []rainviewer.Tile) (map[rainviewer.Tile][]byte, error) {
	out := map[rainviewer.Tile][]byte{}
	for _, t := range tiles {
		d, err := w.FetchTile(ctx, host, path, t)
		if err != nil {
			return nil, err
		}
		out[t] = d
	}
	return out, nil
}

func (w *fakeWorld) FetchCoverage(_ context.Context, _ string, t rainviewer.Tile, _ time.Duration) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.coverage++
	id := fmt.Sprint("cov", w.covered[t])
	if d, ok := w.pngs[id]; ok {
		return d, nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	if !w.covered[t] {
		for i := range img.Pix {
			if i%4 == 3 {
				img.Pix[i] = 255 // opaque black: no radar
			}
		}
	}
	w.pngs[id] = encode(img)
	return w.pngs[id], nil
}

func (w *fakeWorld) tiles(path string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for k, v := range w.fetched {
		if len(k) >= len(path) && k[:len(path)] == path {
			n += v
		}
	}
	return n
}

type fakeStore struct {
	mu      sync.Mutex
	regions map[[2]int]int64
	frames  map[int64]bool
}

func (s *fakeStore) TouchRegion(_ context.Context, r store.Region, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.regions[[2]int{r.TileX, r.TileY}] = now
	return nil
}

func (s *fakeStore) RecordFrame(_ context.Context, t int64, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames[t] = true
	return nil
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// setup builds a world where scout tile (3,1) is covered and rains on z7
// tiles (101,37), (102,37) (adjacent: only one may be picked) and (116,52),
// whose z7 mosaic has no radar.
func setup(t *testing.T) (*fakeWorld, *fakeStore, *Collector, *[]rainviewer.Frame, *time.Time) {
	t.Helper()
	w := newWorld()
	scout := rainviewer.Tile{Z: 2, X: 3, Y: 1}
	w.covered[scout] = true
	w.addRain(scout, "*", rect{5 * 8, 5 * 8, 7 * 8, 6 * 8}) // cells (5,5) and (6,5)
	w.addRain(scout, "*", rect{20 * 8, 20 * 8, 21 * 8, 21 * 8})
	for dy := -1; dy <= 2; dy++ {
		for dx := -1; dx <= 2; dx++ {
			w.covered[rainviewer.Tile{Z: 7, X: 101 + dx, Y: 37 + dy}] = true
		}
	}
	frames := []rainviewer.Frame{{Time: 600, Path: "/a"}, {Time: 1200, Path: "/b"}, {Time: 1800, Path: "/c"}}
	for _, f := range frames {
		for _, x := range []int{101, 102} {
			w.addRain(rainviewer.Tile{Z: 7, X: x, Y: 37}, f.Path, rect{0, 0, 128, 256})
		}
	}
	st := &fakeStore{regions: map[[2]int]int64{}, frames: map[int64]bool{}}
	now := time.Unix(1800, 0)
	feed := func() (string, []rainviewer.Frame) { return "h", frames }
	cfg := DefaultConfig()
	c := New(cfg, w, st, feed, quiet)
	c.now = func() time.Time { return now }
	return w, st, c, &frames, &now
}

func TestTickPicksRainyCoveredNonOverlappingRegions(t *testing.T) {
	w, st, c, _, _ := setup(t)
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := c.Status()
	if len(s.Active) != 1 {
		t.Fatalf("active = %+v, want exactly one region", s.Active)
	}
	r := s.Active[0]
	if r.TileY != 37 || (r.TileX != 101 && r.TileX != 102) {
		t.Errorf("picked %d/%d, want 101 or 102 / 37", r.TileX, r.TileY)
	}
	if r.LastRain.IsZero() {
		t.Error("newest frame rains at z7 but LastRain is zero")
	}
	if r.Climate != "midlat" {
		t.Errorf("climate %q for lat %.1f", r.Climate, r.Lat)
	}
	// Every frame in the feed was cached for the region: 3 frames × 9 tiles.
	for _, p := range []string{"/a", "/b"} {
		if n := w.tiles(p); n != 9 {
			t.Errorf("frame %s: %d tiles fetched, want 9", p, n)
		}
	}
	if len(st.frames) != 3 || len(st.regions) != 1 {
		t.Errorf("store: %d frames, %d regions", len(st.frames), len(st.regions))
	}
	// Only the covered scout tile was downloaded besides coverage.
	if n := w.tiles("/c"); n != 9+1 {
		t.Errorf("newest frame: %d tiles, want 9 + 1 scout tile", n)
	}
}

func TestRegionExpiresAfterHold(t *testing.T) {
	w, _, c, frames, now := setup(t)
	ctx := context.Background()
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	// Rain stops: new frames are dry.
	w.rain = map[rainviewer.Tile]map[string][]rect{}
	for i := 1; i <= 40; i++ {
		ti := 1800 + int64(i)*600
		*frames = append(*frames, rainviewer.Frame{Time: ti, Path: fmt.Sprint("/d", i)})
		*now = time.Unix(ti, 0)
		if err := c.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		active := len(c.Status().Active)
		held := time.Duration(i*600) * time.Second
		if held <= c.cfg.Hold && active != 1 {
			t.Fatalf("dropped after only %s dry", held)
		}
		if held > c.cfg.Hold && active != 0 {
			t.Fatalf("still active %s after the rain stopped", held)
		}
	}
}

func TestDryRegionIsDroppedEarly(t *testing.T) {
	w, _, c, frames, now := setup(t)
	// No rain at z7 although the scout saw some.
	for _, x := range []int{101, 102} {
		delete(w.rain, rainviewer.Tile{Z: 7, X: x, Y: 37})
	}
	ctx := context.Background()
	for i := range 2 {
		if i > 0 {
			*frames = append(*frames, rainviewer.Frame{Time: 2400, Path: "/e"})
			*now = time.Unix(2400, 0)
		}
		if err := c.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range c.Status().Active {
		if r.TileY == 37 {
			t.Fatalf("dry region still active: %+v", r)
		}
	}
}

func TestStatusCountsEvents(t *testing.T) {
	c := New(DefaultConfig(), nil, nil, nil, quiet)
	var h []sample
	for i := range 20 {
		h = append(h, sample{int64(i * 600), i < 7 || (i >= 10 && i < 13)}) // 7-frame event, 3-frame shower
	}
	c.history[key{1, 1}] = h
	s := c.Status()
	if s.Frames24h != 20 || s.Rainy24h != 10 || s.Events24h != 1 {
		t.Errorf("status = %+v, want 20 frames, 10 rainy, 1 event", s)
	}
}

func TestClimate(t *testing.T) {
	for lat, want := range map[float64]string{10.8: "tropical", -20: "tropical", 30: "subtropical", -34.9: "subtropical", 52: "midlat"} {
		if got := Climate(lat); got != want {
			t.Errorf("Climate(%v) = %s, want %s", lat, got, want)
		}
	}
}

func TestBackfillStopsAtBudgetButNewestIsAlwaysFetched(t *testing.T) {
	w, _, c, _, _ := setup(t)
	c.budget = time.Nanosecond
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := w.tiles("/c"); n < 9 {
		t.Errorf("newest frame: %d tiles, want all 9", n)
	}
	if n := w.tiles("/a"); n != 0 {
		t.Errorf("oldest frame fetched (%d tiles) past the budget", n)
	}
}
