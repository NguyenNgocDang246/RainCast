package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"raincast/internal/geocode"
	"raincast/internal/pipeline"
)

type fakeSource struct {
	lat, lon   float64
	notLoaded  bool // the frame index has not been loaded yet
	refreshed  int  // EnsureFresh calls
	frame      int64
	cachedHash string // content hash of the last uploaded tiles
	// serverTiles makes ServerForecast succeed, as when the server can
	// download the tiles itself.
	serverTiles bool
	newest      time.Time // the newest radar frame Radar names
}

func (f *fakeSource) Ready() bool { return !f.notLoaded }

func (f *fakeSource) Status() pipeline.Status { return pipeline.Status{Ticks: 3} }

func (f *fakeSource) NextDue(t time.Time) time.Time { return t.Add(pipeline.FrameInterval) }

func (f *fakeSource) Radar() (pipeline.RadarFrame, bool) {
	if f.notLoaded {
		return pipeline.RadarFrame{}, false
	}
	return pipeline.RadarFrame{Time: f.newest, TileURL: "https://tiles/x/{z}/{x}/{y}.png", MaxZoom: 7}, true
}

func (f *fakeSource) ForecastAt(_ context.Context, lat, lon float64) (*pipeline.Snapshot, error) {
	if f.notLoaded {
		return nil, pipeline.ErrNotReady
	}
	f.lat, f.lon = lat, lon
	return &pipeline.Snapshot{Location: pipeline.Location{Lat: lat, Lon: lon}}, nil
}

func (f *fakeSource) EnsureFresh(context.Context) { f.refreshed++ }

// The fake plan is one tile; its content hash is the tile's bytes.
func (f *fakeSource) ClientPlan(_ context.Context, lat, lon float64) (pipeline.TilePlan, error) {
	if f.notLoaded {
		return pipeline.TilePlan{}, pipeline.ErrNotReady
	}
	return pipeline.TilePlan{Frame: f.frame, Tiles: []pipeline.TileRef{
		{TileID: pipeline.TileID{Time: f.frame, X: 101, Y: 60}, URL: "https://tiles/101/60.png"},
	}}, nil
}

func (f *fakeSource) Cached(_ context.Context, lat, lon float64, hash string) (*pipeline.Snapshot, bool) {
	if hash == "" || hash != f.cachedHash {
		return nil, false
	}
	return &pipeline.Snapshot{Location: pipeline.Location{Lat: lat, Lon: lon}}, true
}

// ServerForecast succeeds when serverTiles is set.
func (f *fakeSource) ServerForecast(_ context.Context, lat, lon float64) (*pipeline.Snapshot, bool) {
	if !f.serverTiles {
		return nil, false
	}
	return &pipeline.Snapshot{Location: pipeline.Location{Lat: lat, Lon: lon}}, true
}

// The fake keeps the last uploaded tile, so naming its hash is enough.
func (f *fakeSource) ForecastFromTiles(_ context.Context, lat, lon float64, tiles map[pipeline.TileID][]byte, sums map[pipeline.TileID]string) (*pipeline.Snapshot, []pipeline.TileID, error) {
	id := pipeline.TileID{Time: f.frame, X: 101, Y: 60}
	for got := range tiles {
		if got.Time != f.frame {
			return nil, nil, pipeline.ErrStaleTiles
		}
	}
	for got := range sums {
		if got.Time != f.frame {
			return nil, nil, pipeline.ErrStaleTiles
		}
	}
	data, ok := tiles[id]
	if !ok {
		if sums[id] == "" || sums[id] != sha(f.cachedHash) {
			return nil, []pipeline.TileID{id}, nil
		}
		data = []byte(f.cachedHash)
	}
	if len(tiles) > 1 || string(data) == "bad" {
		return nil, nil, pipeline.ErrBadTiles
	}
	f.cachedHash = string(data)
	return &pipeline.Snapshot{Location: pipeline.Location{Lat: lat, Lon: lon}}, nil, nil
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type fakeGeo struct{}

func (fakeGeo) Resolve(_ context.Context, q, country string) ([]geocode.Place, error) {
	if q == "link" {
		return nil, geocode.ErrUnresolvedLink
	}
	return []geocode.Place{{Name: q, Address: country, Lat: 1, Lon: 2}}, nil
}

func (fakeGeo) Stats() geocode.Stats { return geocode.Stats{SearchOK: 7} }

func (fakeGeo) Reverse(_ context.Context, lat, lon float64) (geocode.Place, error) {
	return geocode.Place{Name: "here", Lat: lat, Lon: lon}, nil
}

func (fakeGeo) Tile(_ context.Context, z, x, y int) ([]byte, error) {
	if z > 18 {
		return nil, geocode.ErrBadTile
	}
	return []byte("png"), nil
}

func (fakeGeo) Suggest(_ context.Context, q string, bias *geocode.LatLon, country string) ([]geocode.Place, error) {
	name := q
	if bias != nil {
		name += " near"
	}
	return []geocode.Place{{Name: name, Address: country}}, nil
}

func do(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestForecastEndpoint(t *testing.T) {
	src := &fakeSource{notLoaded: true}
	h := New(src, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{})

	if rec := do(t, h, "/api/forecast?lat=10&lon=106"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("before the frame index loads: %d", rec.Code)
	}
	src.notLoaded = false
	// There is no default location: coordinates are required.
	for _, q := range []string{"", "?lat=10", "?lon=106"} {
		if rec := do(t, h, "/api/forecast"+q); rec.Code != http.StatusBadRequest {
			t.Errorf("%q: %d, want 400", q, rec.Code)
		}
	}
	if rec := do(t, h, "/api/forecast?lat=10.123456&lon=106.654321"); rec.Code != http.StatusOK {
		t.Fatalf("on demand: %d %s", rec.Code, rec.Body)
	}
	if src.lat != 10.1235 || src.lon != 106.6543 {
		t.Errorf("not rounded: %v %v", src.lat, src.lon)
	}
	for _, q := range []string{"lat=91&lon=0", "lat=x&lon=1", "lat=10"} {
		if rec := do(t, h, "/api/forecast?"+q); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
}

func TestGeocodeEndpoint(t *testing.T) {
	h := New(&fakeSource{}, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{})
	rec := do(t, h, "/api/geocode?q=Th%E1%BB%A7+%C4%90%E1%BB%A9c")
	var ps []geocode.Place
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &ps) != nil || ps[0].Name != "Thủ Đức" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "/api/geocode"); rec.Code != http.StatusBadRequest {
		t.Errorf("empty q: %d", rec.Code)
	}
	if rec := do(t, h, "/api/geocode?q=link"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad link: %d", rec.Code)
	}
}

func TestSuggestEndpoint(t *testing.T) {
	h := New(&fakeSource{}, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{})
	for path, want := range map[string]string{
		"/api/suggest?q=ben":                    "ben",
		"/api/suggest?q=ben&lat=10.8&lon=106.7": "ben near",
		"/api/suggest?q=ben&lat=x&lon=106.7":    "ben",
	} {
		rec := do(t, h, path)
		var ps []geocode.Place
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &ps) != nil || ps[0].Name != want {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestSearchUsesClientCountry(t *testing.T) {
	h := New(&fakeSource{}, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{
		CountryOf: func(ip netip.Addr) string {
			return map[string]string{"203.0.113.5": "sg", "198.51.100.7": "vn"}[ip.String()]
		},
	})
	for _, c := range []struct {
		name, remote, xff, want string
	}{
		{"direct client", "203.0.113.5:4000", "", "sg"},
		{"direct client cannot spoof", "198.51.100.7:4000", "203.0.113.5", "vn"},
		{"behind proxy", "172.18.0.4:5000", "198.51.100.7, 203.0.113.5", "sg"},
		{"behind proxy, IPv4-mapped peer", "[::ffff:127.0.0.1]:5000", "203.0.113.5", "sg"},
		{"proxy without header", "172.18.0.4:5000", "", ""},
	} {
		for _, path := range []string{"/api/suggest?q=ben", "/api/geocode?q=ben"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = c.remote
			if c.xff != "" {
				req.Header.Set("X-Forwarded-For", c.xff)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			var ps []geocode.Place
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &ps) != nil || ps[0].Address != c.want {
				t.Errorf("%s %s: %d %s, want country %q", c.name, path, rec.Code, rec.Body, c.want)
			}
		}
	}
}

func TestReverseEndpoint(t *testing.T) {
	h := New(&fakeSource{}, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{})
	rec := do(t, h, "/api/reverse?lat=10.123456&lon=106.7")
	var p geocode.Place
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &p) != nil || p.Name != "here" || p.Lat != 10.1235 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "/api/reverse?lat=91&lon=0"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad coords: %d", rec.Code)
	}
}

func TestTileEndpoint(t *testing.T) {
	h := New(&fakeSource{}, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{})
	rec := do(t, h, "/api/tiles/5/24/14.png")
	if rec.Code != http.StatusOK || rec.Body.String() != "png" || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/tiles/19/0/0", "/api/tiles/a/0/0"} {
		if rec := do(t, h, path); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
}

func TestRadarEndpoint(t *testing.T) {
	src := &fakeSource{notLoaded: true}
	h := New(src, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{})
	if rec := do(t, h, "/api/radar"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("before the frame index loads: %d", rec.Code)
	}
	src.notLoaded = false
	rec := do(t, h, "/api/radar")
	var f pipeline.RadarFrame
	if err := json.NewDecoder(rec.Body).Decode(&f); err != nil || rec.Code != http.StatusOK || f.MaxZoom != 7 || f.TileURL == "" {
		t.Errorf("%d %+v %v", rec.Code, f, err)
	}
}

func TestRadarMaxAge(t *testing.T) {
	due := time.Date(2026, 10, 5, 8, 10, 0, 0, time.UTC)
	for _, c := range []struct {
		before time.Duration
		want   int
	}{
		{8 * time.Minute, 60},
		{30 * time.Second, 30},
		{0, 15},
		{-4 * time.Minute, 15},
	} {
		if got := radarMaxAge(due, due.Add(-c.before)); got != c.want {
			t.Errorf("%v before due: %d, want %d", c.before, got, c.want)
		}
	}
}
