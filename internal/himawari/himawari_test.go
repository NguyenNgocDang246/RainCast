package himawari

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseScans(t *testing.T) {
	got, err := parseScans([]byte(`[{"basetime":"20261004051000","validtime":"20261004051000"},{"basetime":"20261004052000","validtime":"x"}]`))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 4, 5, 10, 0, 0, time.UTC).Unix()
	if len(got) != 2 || got[0] != want || got[1] != want+ScanStep {
		t.Fatalf("got %v", got)
	}
	if ScanName(want) != "20261004051000" {
		t.Fatalf("name %s", ScanName(want))
	}
}

// A forecast never uses a scan that started after its radar frame, nor one
// JMA could not have published yet.
func TestCandidatesBeforeFrame(t *testing.T) {
	frame := time.Date(2026, 10, 5, 8, 10, 0, 0, time.UTC).Unix()
	c := Candidates(frame)
	if len(c) != 2 || c[0] != frame-600 || c[1] != frame-1200 {
		t.Fatalf("got %v", c)
	}
	// An off-grid frame time rounds down first.
	if c := Candidates(frame + 120); c[0] != frame-600 {
		t.Fatalf("got %v", c)
	}
}

func TestCovers(t *testing.T) {
	for _, tc := range []struct {
		lat, lon float64
		want     bool
	}{{10.8, 106.7, true}, {35, 139, true}, {-25, 135, true}, {21, -157, true}, {48, 2, false}, {-15, -49, false}, {70, 140, false}} {
		if got := Covers(tc.lat, tc.lon); got != tc.want {
			t.Errorf("Covers(%v, %v) = %v", tc.lat, tc.lon, got)
		}
	}
}

func TestRegionTiles(t *testing.T) {
	// Radar tile (101, 60) at z7: its mosaic spans z7 pixels 100·256 …
	// 103·256 across (z5 6400 … 6591, tile 25) and 59·256 … 62·256 down
	// (z5 3776 … 3967, tiles 14 and 15).
	got := RegionTiles(101, 60, 0)
	if len(got) != 2 || got[0] != (Tile{25, 14}) || got[1] != (Tile{25, 15}) {
		t.Fatalf("got %v", got)
	}
	// A margin reaching over a tile edge adds the neighbors.
	if got := RegionTiles(101, 60, 70); len(got) != 6 {
		t.Fatalf("got %v", got)
	}
	// Across the antimeridian x wraps.
	for _, tl := range RegionTiles(0, 60, 40) {
		if tl.X < 0 || tl.X >= 1<<Zoom {
			t.Fatalf("unwrapped %v", tl)
		}
	}
}

func grayJPEG(t *testing.T, v uint8) []byte {
	g := image.NewGray(image.Rect(0, 0, 256, 256))
	for i := range g.Pix {
		g.Pix[i] = v
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, g, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestFetchCachesAndRemembersMissing(t *testing.T) {
	var hits atomic.Int64
	tile := grayJPEG(t, 200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.Contains(r.URL.Path, "/25/15.jpg") {
			w.Write(tile)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := New(t.TempDir(), 1000)
	c.BaseURL = srv.URL
	ctx := context.Background()
	scan := time.Now().Add(-3 * time.Hour).Unix()
	scan -= scan % ScanStep
	for range 2 {
		if _, err := c.Fetch(ctx, scan, Tile{25, 15}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Fetch(ctx, scan, Tile{26, 15}); !errors.Is(err, ErrMissing) {
			t.Fatalf("want missing, got %v", err)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("%d requests, want 2 (both answers cached)", hits.Load())
	}
	f := c.LoadFrame(scan, []Tile{{25, 15}, {26, 15}})
	if f == nil {
		t.Fatal("no frame")
	}
	v, ok := f.At(25*256+100, 15*256+100)
	if !ok || v < 195 || v > 205 {
		t.Fatalf("At = %v, %v", v, ok)
	}
	if _, ok := f.At(26*256+100, 15*256+100); ok {
		t.Fatal("missing tile read")
	}
}

func TestThrottleCoolsDown(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := New(t.TempDir(), 1000)
	c.BaseURL = srv.URL
	ctx := context.Background()
	if _, err := c.Fetch(ctx, 1791000000, Tile{1, 1}); err == nil {
		t.Fatal("want error")
	}
	if _, err := c.Fetch(ctx, 1791000000, Tile{1, 2}); err == nil {
		t.Fatal("want error")
	}
	if hits.Load() != 1 {
		t.Fatalf("%d requests, want 1: the client must stop after a 429", hits.Load())
	}
	if c.Stats().Throttled != 1 {
		t.Fatalf("stats %+v", c.Stats())
	}
}
