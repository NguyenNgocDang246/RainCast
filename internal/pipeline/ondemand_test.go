package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"raincast/internal/rainviewer"
)

// A point in tile 101/60 at z7 (Ho Chi Minh City).
const testLat, testLon = 10.85, 106.77

// fakeRainViewer serves an index of five frames 10 minutes apart; every
// tile is one of two real radar tiles, alternating by frame.
type fakeRainViewer struct {
	srv    *httptest.Server
	newest atomic.Int64
	tiles  atomic.Int64 // tile requests served
}

func newFakeRainViewer(t *testing.T) *fakeRainViewer {
	t.Helper()
	a, err := os.ReadFile("../radar/testdata/tile_7_101_60.png")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../radar/testdata/tile_7_101_60_1840.png")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRainViewer{}
	f.newest.Store(1_790_000_400)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/maps.json" {
			var past []string
			for i := int64(4); i >= 0; i-- {
				tm := f.newest.Load() - i*600
				past = append(past, fmt.Sprintf(`{"time":%d,"path":"/v2/radar/f%d"}`, tm, tm))
			}
			fmt.Fprintf(w, `{"generated":1,"host":%q,"radar":{"past":[%s]}}`, f.srv.URL, strings.Join(past, ","))
			return
		}
		f.tiles.Add(1)
		var tm int64
		fmt.Sscanf(r.URL.Path, "/v2/radar/f%d/", &tm)
		if tm/600%2 == 0 {
			w.Write(a)
		} else {
			w.Write(b)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func testPipeline(t *testing.T, rv *fakeRainViewer, shared *mapCache) *Pipeline {
	t.Helper()
	cfg := DefaultConfig()
	cfg.IndexMaxAge = time.Minute
	if shared != nil {
		cfg.Shared = shared
	}
	client := rainviewer.New("")
	client.MapsURL = rv.srv.URL + "/maps.json"
	p := New(cfg, client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.EnsureFresh(context.Background())
	if !p.Ready() {
		t.Fatal("index not loaded")
	}
	return p
}

// download fetches every tile of plan as a browser would.
func download(t *testing.T, plan TilePlan) map[TileID][]byte {
	t.Helper()
	out := map[TileID][]byte{}
	for _, ref := range plan.Tiles {
		resp, err := http.Get(ref.URL)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		out[ref.TileID] = data
	}
	return out
}

func planOrder(plan TilePlan) []TileID {
	var ids []TileID
	for _, r := range plan.Tiles {
		ids = append(ids, r.TileID)
	}
	return ids
}

func copyTiles(tiles map[TileID][]byte) map[TileID][]byte {
	out := make(map[TileID][]byte, len(tiles))
	for id, d := range tiles {
		out[id] = d
	}
	return out
}

func sameForecast(a, b *Snapshot) bool {
	return a.ArrivalMin == b.ArrivalMin && a.SpeedKmh == b.SpeedKmh && a.FrameTime == b.FrameTime &&
		fmt.Sprint(a.Series) == fmt.Sprint(b.Series)
}

// Tiles the client downloads give the same forecast as tiles the server
// downloads itself, and are then cached under their content hash.
func TestForecastFromTilesMatchesServerFetch(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	p := testPipeline(t, rv, nil)
	plan, err := p.Plan(testLat, testLon)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tiles) != 5*4 || plan.Frame != rv.newest.Load() {
		t.Fatalf("plan = %d tiles, frame %d", len(plan.Tiles), plan.Frame)
	}
	tiles := download(t, plan)
	hash := ContentHash(planOrder(plan), tiles)
	if _, ok := p.Cached(ctx, testLat, testLon, hash); ok {
		t.Fatal("cached before any upload")
	}
	got, err := p.ForecastFromTiles(ctx, testLat, testLon, tiles)
	if err != nil {
		t.Fatal(err)
	}
	want, err := testPipeline(t, rv, nil).ForecastAt(ctx, testLat, testLon)
	if err != nil {
		t.Fatal(err)
	}
	if !sameForecast(got, want) {
		t.Fatalf("from tiles %+v\nserver fetch %+v", got.Result, want.Result)
	}
	hit, ok := p.Cached(ctx, testLat+0.01, testLon, hash)
	if !ok || hit.FrameTime != got.FrameTime {
		t.Fatal("upload not cached by its hash")
	}
}

// Tampered tiles are cached apart: the genuine hash never serves them.
func TestTamperedTilesStayApart(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	p := testPipeline(t, rv, nil)
	plan, _ := p.Plan(testLat, testLon)
	genuine := download(t, plan)
	forged := copyTiles(genuine)
	empty := emptyTile(t)
	for _, id := range planOrder(plan)[16:] { // blank out the newest frame
		forged[id] = empty
	}
	if _, err := p.ForecastFromTiles(ctx, testLat, testLon, forged); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Cached(ctx, testLat, testLon, ContentHash(planOrder(plan), genuine)); ok {
		t.Fatal("forged upload served under the genuine hash")
	}
}

func TestForecastFromTilesRejects(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	p := testPipeline(t, rv, nil)
	plan, _ := p.Plan(testLat, testLon)
	tiles := download(t, plan)

	missing := copyTiles(tiles)
	delete(missing, plan.Tiles[0].TileID)
	if _, err := p.ForecastFromTiles(ctx, testLat, testLon, missing); !errors.Is(err, ErrBadTiles) {
		t.Errorf("missing tile: err = %v", err)
	}

	big := copyTiles(tiles)
	var buf bytes.Buffer
	png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 512, 512)))
	big[plan.Tiles[3].TileID] = buf.Bytes()
	if _, err := p.ForecastFromTiles(ctx, testLat, testLon, big); !errors.Is(err, ErrBadTiles) {
		t.Errorf("512 px tile: err = %v", err)
	}

	// A new frame arrives: the old tiles are stale.
	rv.newest.Add(600)
	p.Tick(ctx)
	if _, err := p.ForecastFromTiles(ctx, testLat, testLon, tiles); !errors.Is(err, ErrStaleTiles) {
		t.Errorf("old frames: err = %v", err)
	}
}

// Another process finds a region and the frame index in the shared cache
// without downloading anything.
func TestSharedCacheServesOtherProcess(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	shared := &mapCache{m: map[string][]byte{}}
	p1 := testPipeline(t, rv, shared)
	want, err := p1.ForecastAt(ctx, testLat, testLon)
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := p1.Plan(testLat, testLon)
	tiles := download(t, plan)
	if _, err := p1.ForecastFromTiles(ctx, testLat, testLon, tiles); err != nil {
		t.Fatal(err)
	}
	served := rv.tiles.Load()

	p2 := testPipeline(t, rv, shared)
	got, err := p2.ForecastAt(ctx, testLat, testLon)
	if err != nil {
		t.Fatal(err)
	}
	if rv.tiles.Load() != served {
		t.Error("second process downloaded tiles the shared cache holds")
	}
	if !sameForecast(got, want) {
		t.Fatalf("shared region forecasts %+v, want %+v", got.Result, want.Result)
	}
	if _, ok := p2.Cached(ctx, testLat, testLon, ContentHash(planOrder(plan), tiles)); !ok {
		t.Error("uploaded region not shared")
	}
	if !shared.has("rc:index") || !shared.hasPrefix("rc:motion:") {
		t.Errorf("shared keys = %v", shared.keys())
	}
}

func emptyTile(t *testing.T) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 256, 256))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// mapCache is a cache.Shared in memory.
type mapCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (c *mapCache) Get(_ context.Context, key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[key]
	return v, ok
}

func (c *mapCache) Set(_ context.Context, key string, val []byte, _ time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = val
}

func (c *mapCache) has(key string) bool {
	_, ok := c.Get(context.Background(), key)
	return ok
}

func (c *mapCache) hasPrefix(prefix string) bool {
	for _, k := range c.keys() {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func (c *mapCache) keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for k := range c.m {
		out = append(out, k)
	}
	return out
}
