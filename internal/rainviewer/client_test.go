package rainviewer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(t.TempDir())
	c.MapsURL = srv.URL + "/maps"
	c.Backoff = time.Millisecond
	return c, srv
}

func TestFetchMaps(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"host":"https://h","radar":{"past":[{"time":600,"path":"/v2/radar/a"}],"nowcast":[]}}`))
	})
	m, err := c.FetchMaps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if m.Host != "https://h" || len(m.Radar.Past) != 1 || m.Radar.Past[0].Path != "/v2/radar/a" {
		t.Fatalf("maps = %+v", m)
	}
}

func TestFetchTileRetriesAndCaches(t *testing.T) {
	var calls atomic.Int32
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v2/radar/a/256/7/101/60/2/0_0.png") {
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("png"))
	})
	tiles, err := c.FetchTiles(context.Background(), srv.URL, "/v2/radar/a", []Tile{{7, 101, 60}})
	if err != nil {
		t.Fatal(err)
	}
	if string(tiles[Tile{7, 101, 60}]) != "png" || calls.Load() != 2 {
		t.Fatalf("data=%q calls=%d", tiles[Tile{7, 101, 60}], calls.Load())
	}
	// Second fetch comes from the disk cache.
	if _, err := c.FetchTile(context.Background(), srv.URL, "/v2/radar/a", Tile{7, 101, 60}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("cache miss: calls=%d", calls.Load())
	}
}

func TestFetchTileNoRetryOn404(t *testing.T) {
	var calls atomic.Int32
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	})
	if _, err := c.FetchTile(context.Background(), srv.URL, "/x", Tile{7, 1, 1}); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestFetchTileRejectsZoom(t *testing.T) {
	c := New("")
	if _, err := c.FetchTile(context.Background(), "http://x", "/p", Tile{8, 1, 1}); err == nil {
		t.Fatal("expected zoom error")
	}
}
