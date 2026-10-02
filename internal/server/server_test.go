package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"raincast/internal/geocode"
	"raincast/internal/pipeline"
)

type fakeSource struct {
	lat, lon  float64
	notLoaded bool // the frame index has not been loaded yet
}

func (f *fakeSource) Ready() bool { return !f.notLoaded }

func (f *fakeSource) Status() pipeline.Status { return pipeline.Status{Ticks: 3} }

func (f *fakeSource) Radar() (pipeline.RadarFrame, bool) {
	if f.notLoaded {
		return pipeline.RadarFrame{}, false
	}
	return pipeline.RadarFrame{TileURL: "https://tiles/x/{z}/{x}/{y}.png", MaxZoom: 7}, true
}

func (f *fakeSource) ForecastAt(_ context.Context, lat, lon float64) (*pipeline.Snapshot, error) {
	if f.notLoaded {
		return nil, pipeline.ErrNotReady
	}
	f.lat, f.lon = lat, lon
	return &pipeline.Snapshot{Location: pipeline.Location{Lat: lat, Lon: lon}}, nil
}

type fakeGeo struct{}

func (fakeGeo) Resolve(_ context.Context, q string) ([]geocode.Place, error) {
	if q == "link" {
		return nil, geocode.ErrUnresolvedLink
	}
	return []geocode.Place{{Name: q, Lat: 1, Lon: 2}}, nil
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

func (fakeGeo) Suggest(_ context.Context, q string, bias *geocode.LatLon) ([]geocode.Place, error) {
	name := q
	if bias != nil {
		name += " near"
	}
	return []geocode.Place{{Name: name}}, nil
}

func do(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestForecastEndpoint(t *testing.T) {
	src := &fakeSource{notLoaded: true}
	h := New(src, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{HorizonMin: 60})

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
	h := New(&fakeSource{}, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{HorizonMin: 60})
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
	h := New(&fakeSource{}, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{HorizonMin: 60})
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

func TestReverseEndpoint(t *testing.T) {
	h := New(&fakeSource{}, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{HorizonMin: 60})
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
	h := New(&fakeSource{}, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{HorizonMin: 60})
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
	h := New(src, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{HorizonMin: 60})
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
