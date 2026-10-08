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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"raincast/internal/rainviewer"
)

// A point in tile 101/60 at z7 (Ho Chi Minh City).
const testLat, testLon = 10.85, 106.77

// fakeRainViewer serves an index of seven frames 10 minutes apart; every
// tile is one of two real radar tiles, alternating by frame.
type fakeRainViewer struct {
	srv    *httptest.Server
	newest atomic.Int64
	tiles  atomic.Int64 // tile requests served
	// limited answers tile requests with 429, as RainViewer over its limit.
	limited atomic.Bool
	// slow, when set, is a frame time whose tiles answer only after the
	// request is given up.
	slow atomic.Int64
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
			for i := int64(6); i >= 0; i-- {
				tm := f.newest.Load() - i*600
				past = append(past, fmt.Sprintf(`{"time":%d,"path":"/v2/radar/f%d"}`, tm, tm))
			}
			fmt.Fprintf(w, `{"generated":1,"host":%q,"radar":{"past":[%s]}}`, f.srv.URL, strings.Join(past, ","))
			return
		}
		if slow := f.slow.Load(); slow != 0 && strings.Contains(r.URL.Path, fmt.Sprintf("/f%d/", slow)) {
			<-r.Context().Done()
			return
		}
		if f.limited.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
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
		// Bytes after IEND, which decoders ignore, make every frame's tile
		// distinct, as RainViewer's are.
		fmt.Fprint(w, tm)
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
	got, _, err := p.ForecastFromTiles(ctx, testLat, testLon, tiles, nil)
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
	if _, _, err := p.ForecastFromTiles(ctx, testLat, testLon, forged, nil); err != nil {
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

	partial := copyTiles(tiles)
	delete(partial, plan.Tiles[0].TileID)
	if _, missing, err := p.ForecastFromTiles(ctx, testLat, testLon, partial, nil); err != nil || len(missing) != 1 || missing[0] != plan.Tiles[0].TileID {
		t.Errorf("one tile left out: missing %v, err = %v", missing, err)
	}

	big := copyTiles(tiles)
	var buf bytes.Buffer
	png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 512, 512)))
	big[plan.Tiles[3].TileID] = buf.Bytes()
	if _, _, err := p.ForecastFromTiles(ctx, testLat, testLon, big, nil); !errors.Is(err, ErrBadTiles) {
		t.Errorf("512 px tile: err = %v", err)
	}

	// Three new frames: the old plan's frames have left the index.
	rv.newest.Add(3 * 600)
	p.Tick(ctx)
	if _, _, err := p.ForecastFromTiles(ctx, testLat, testLon, tiles, nil); !errors.Is(err, ErrStaleTiles) {
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
	if _, _, err := p1.ForecastFromTiles(ctx, testLat, testLon, tiles, nil); err != nil {
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

// sumsOf names every tile by its hash, as a client does first.
func sumsOf(tiles map[TileID][]byte) map[TileID]string {
	out := make(map[TileID]string, len(tiles))
	for id, d := range tiles {
		out[id] = tileSum(d)
	}
	return out
}

// After a new frame a client that names its tiles by hash uploads only the
// new frame's; a nearby point in the same block uploads nothing.
func TestUploadOnlyMissingTiles(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	p := testPipeline(t, rv, nil)
	plan, _ := p.Plan(testLat, testLon)
	tiles := download(t, plan)
	if _, missing, err := p.ForecastFromTiles(ctx, testLat, testLon, nil, sumsOf(tiles)); err != nil || len(missing) != len(plan.Tiles) {
		t.Fatalf("first request: %d missing, err = %v", len(missing), err)
	}
	if _, _, err := p.ForecastFromTiles(ctx, testLat, testLon, tiles, nil); err != nil {
		t.Fatal(err)
	}

	rv.newest.Add(600)
	p.Tick(ctx)
	plan, _ = p.Plan(testLat, testLon)
	tiles = download(t, plan)
	_, missing, err := p.ForecastFromTiles(ctx, testLat, testLon, nil, sumsOf(tiles))
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 4 {
		t.Fatalf("missing %v, want the newest frame's 4 tiles", missing)
	}
	for _, id := range missing {
		if id.Time != plan.Frame {
			t.Fatalf("missing %v, an older frame's tile was uploaded before", id)
		}
	}
	upload := map[TileID][]byte{}
	for _, id := range missing {
		upload[id] = tiles[id]
	}
	got, missing, err := p.ForecastFromTiles(ctx, testLat, testLon, upload, sumsOf(tiles))
	if err != nil || len(missing) > 0 {
		t.Fatalf("missing %v, err = %v", missing, err)
	}
	want, err := testPipeline(t, rv, nil).ForecastAt(ctx, testLat, testLon)
	if err != nil {
		t.Fatal(err)
	}
	if !sameForecast(got, want) {
		t.Fatalf("from kept tiles %+v\nserver fetch %+v", got.Result, want.Result)
	}
	if _, missing, err := p.ForecastFromTiles(ctx, testLat+0.01, testLon, nil, sumsOf(tiles)); err != nil || len(missing) > 0 {
		t.Fatalf("same block again: missing %v, err = %v", missing, err)
	}
}

// A forged upload is kept only under its own hash: a client naming the
// genuine tile's hash is asked to upload it.
func TestForgedUploadNeverStandsIn(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	p := testPipeline(t, rv, nil)
	plan, _ := p.Plan(testLat, testLon)
	genuine := download(t, plan)
	forged := copyTiles(genuine)
	newest := planOrder(plan)[16:]
	for _, id := range newest {
		forged[id] = emptyTile(t)
	}
	if _, _, err := p.ForecastFromTiles(ctx, testLat, testLon, forged, nil); err != nil {
		t.Fatal(err)
	}
	_, missing, err := p.ForecastFromTiles(ctx, testLat, testLon, nil, sumsOf(genuine))
	if err != nil || len(missing) != len(newest) {
		t.Fatalf("missing %v, err = %v; want the newest frame's tiles", missing, err)
	}
	// A hash nothing was uploaded under is missing too.
	sums := sumsOf(genuine)
	sums[planOrder(plan)[0]] = strings.Repeat("ab", 32)
	if _, missing, _ := p.ForecastFromTiles(ctx, testLat, testLon, nil, sums); !slices.Contains(missing, planOrder(plan)[0]) {
		t.Fatalf("unknown hash not missing: %v", missing)
	}
}

func serverFetchPipeline(t *testing.T, rv *fakeRainViewer, perMinute int, shared *mapCache) *Pipeline {
	t.Helper()
	cfg := DefaultConfig()
	cfg.IndexMaxAge = time.Minute
	cfg.ServerFetchPerMinute = perMinute
	if shared != nil {
		cfg.Shared = shared
	}
	client := rainviewer.New("")
	client.MapsURL = rv.srv.URL + "/maps.json"
	p := New(cfg, client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.EnsureFresh(context.Background())
	return p
}

// Within its budget the server downloads the tiles itself, and after a new
// frame only the new frame's.
func TestServerForecastWithinBudget(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	p := serverFetchPipeline(t, rv, 30, nil)
	got, ok := p.ServerForecast(ctx, testLat, testLon)
	if !ok {
		t.Fatal("server forecast refused within budget")
	}
	want, err := testPipeline(t, rv, nil).ForecastAt(ctx, testLat, testLon)
	if err != nil {
		t.Fatal(err)
	}
	if !sameForecast(got, want) {
		t.Fatalf("server forecast %+v, want %+v", got.Result, want.Result)
	}
	rv.newest.Add(600)
	p.Tick(ctx)
	before := rv.tiles.Load()
	if _, ok := p.ServerForecast(ctx, testLat, testLon); !ok {
		t.Fatal("refused after a new frame")
	}
	if n := rv.tiles.Load() - before; n != 4 {
		t.Fatalf("downloaded %d tiles after a new frame, want 4", n)
	}
	// 24 of 30 spent: a new block's 20 tiles are over budget.
	if _, ok := p.ServerForecast(ctx, 21.03, 105.85); ok {
		t.Fatal("downloaded over budget")
	}
}

// A 429 stops server downloads in every process sharing the cache.
func TestServerForecastCoolsDownOn429(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	if _, ok := serverFetchPipeline(t, rv, 0, nil).ServerForecast(ctx, testLat, testLon); ok {
		t.Fatal("served without a budget")
	}
	shared := &mapCache{m: map[string][]byte{}}
	p1 := serverFetchPipeline(t, rv, 100, shared)
	rv.limited.Store(true)
	if _, ok := p1.ServerForecast(ctx, testLat, testLon); ok {
		t.Fatal("served although RainViewer refused")
	}
	if !shared.has(cooldownKey) {
		t.Fatal("429 not shared")
	}
	rv.limited.Store(false)
	before := rv.tiles.Load()
	p2 := serverFetchPipeline(t, rv, 100, shared)
	if _, ok := p2.ServerForecast(ctx, testLat, testLon); ok || rv.tiles.Load() != before {
		t.Fatal("another process downloaded during the cooldown")
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

// A plan offers the hash of every tile the server has, so after a new
// frame a client uploads only the new frame's tiles in one POST.
func TestClientPlanOffersKnownSums(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	p := testPipeline(t, rv, nil)
	plan, _ := p.ClientPlan(ctx, testLat, testLon)
	for _, ref := range plan.Tiles {
		if ref.Sum != "" {
			t.Fatalf("offered %v before any upload", ref.TileID)
		}
	}
	tiles := download(t, plan)
	if _, _, err := p.ForecastFromTiles(ctx, testLat, testLon, tiles, nil); err != nil {
		t.Fatal(err)
	}
	rv.newest.Add(600)
	p.Tick(ctx)
	plan, _ = p.ClientPlan(ctx, testLat, testLon)
	tiles = download(t, plan)
	upload := map[TileID][]byte{}
	sums := map[TileID]string{}
	for _, ref := range plan.Tiles {
		if ref.Sum == tileSum(tiles[ref.TileID]) {
			sums[ref.TileID] = ref.Sum
		} else {
			upload[ref.TileID] = tiles[ref.TileID]
		}
	}
	if len(upload) != 4 {
		t.Fatalf("would upload %d tiles, want the newest frame's 4", len(upload))
	}
	if _, missing, err := p.ForecastFromTiles(ctx, testLat, testLon, upload, sums); err != nil || len(missing) > 0 {
		t.Fatalf("missing %v, err = %v", missing, err)
	}
}

// Tiles that arrived before the timeout are kept: the plan offers them, so
// the client uploads only the late ones.
func TestServerForecastKeepsTilesOnTimeout(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	p := serverFetchPipeline(t, rv, 100, nil)
	rv.slow.Store(rv.newest.Load())
	start := time.Now()
	if _, ok := p.ServerForecast(ctx, testLat, testLon); ok {
		t.Fatal("served without the newest frame")
	}
	if d := time.Since(start); d > serverFetchTimeout+time.Second {
		t.Fatalf("gave up after %s", d)
	}
	plan, _ := p.ClientPlan(ctx, testLat, testLon)
	known := 0
	for _, ref := range plan.Tiles {
		if ref.Sum != "" {
			known++
			if ref.Time == rv.newest.Load() {
				t.Fatalf("offered late tile %v", ref.TileID)
			}
		}
	}
	if known != 16 {
		t.Fatalf("kept %d tiles, want the 16 of older frames", known)
	}
}

// Tiles a frame behind still make a forecast, from their own frames.
func TestForecastFromTilesFrameBehind(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	p := testPipeline(t, rv, nil)
	want, err := testPipeline(t, rv, nil).ForecastAt(ctx, testLat, testLon)
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := p.Plan(testLat, testLon)
	tiles := download(t, plan)
	rv.newest.Add(600)
	p.Tick(ctx)
	got, missing, err := p.ForecastFromTiles(ctx, testLat, testLon, tiles, nil)
	if err != nil || len(missing) > 0 {
		t.Fatalf("missing %v, err = %v", missing, err)
	}
	if !sameForecast(got, want) || got.FrameTime.Unix() != plan.Frame {
		t.Fatalf("frame behind forecasts %+v at %v, want %+v", got.Result, got.FrameTime, want.Result)
	}
	// Named by hash, the same tiles hit the region just built.
	if snap, missing, err := p.ForecastFromTiles(ctx, testLat, testLon, nil, sumsOf(tiles)); err != nil || len(missing) > 0 || snap.FrameTime != got.FrameTime {
		t.Fatalf("by hash: missing %v, err = %v", missing, err)
	}
}
