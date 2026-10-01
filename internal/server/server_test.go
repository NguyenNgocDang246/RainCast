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

type fakeSource struct{ lat, lon float64 }

func (f *fakeSource) Latest() *pipeline.Snapshot { return nil }

func (f *fakeSource) Status() pipeline.Status { return pipeline.Status{Ticks: 3} }

func (f *fakeSource) ForecastAt(_ context.Context, lat, lon float64) (*pipeline.Snapshot, error) {
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

func (fakeGeo) Stats() geocode.Stats { return geocode.Stats{PhotonOK: 7} }

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
	src := &fakeSource{}
	h := New(src, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{HorizonMin: 60})

	if rec := do(t, h, "/api/forecast"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("home before ready: %d", rec.Code)
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
