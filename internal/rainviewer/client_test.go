package rainviewer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
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

func TestLimiterPacesRequests(t *testing.T) {
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) })
	c.CacheDir = ""
	// 20/s with a burst of 1: five requests take at least 200 ms.
	c.Limiter = rate.NewLimiter(20, 1)
	c.CollectLimiter = nil
	start := time.Now()
	for i := range 5 {
		if _, err := c.FetchTile(context.Background(), srv.URL, "/p", Tile{Z: 7, X: i}); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 180*time.Millisecond {
		t.Errorf("5 requests took %s; the limiter did not pace them", d)
	}
	if st := c.Stats(); st.LastMinute != 5 {
		t.Errorf("stats = %+v, want 5 in the last minute", st)
	}
}

func TestCollectingUsesItsOwnLimiter(t *testing.T) {
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) })
	c.CacheDir = ""
	c.Limiter = nil
	c.CollectLimiter = rate.NewLimiter(20, 1)
	fetch := func(ctx context.Context) time.Duration {
		start := time.Now()
		for i := range 5 {
			if _, err := c.FetchTile(ctx, srv.URL, "/p", Tile{Z: 7, X: i}); err != nil {
				t.Fatal(err)
			}
		}
		return time.Since(start)
	}
	if d := fetch(context.Background()); d > 150*time.Millisecond {
		t.Errorf("interactive requests were paced by the collect limiter (%s)", d)
	}
	if d := fetch(Collecting(context.Background())); d < 180*time.Millisecond {
		t.Errorf("collecting requests were not paced (%s)", d)
	}
}

func TestRetryAfterIsHonored(t *testing.T) {
	var calls atomic.Int32
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte("x"))
	})
	start := time.Now()
	if _, err := c.FetchTile(context.Background(), srv.URL, "/p", Tile{Z: 7}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 900*time.Millisecond {
		t.Errorf("retried after %s, want ≥ 1 s", d)
	}
}

func TestFetchCoverageCachesAndSurvivesPrune(t *testing.T) {
	var calls atomic.Int32
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/coverage/0/256/7/101/60/0/0_0.png" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		w.Write([]byte("cov"))
	})
	tile := Tile{Z: 7, X: 101, Y: 60}
	for range 2 {
		data, err := c.FetchCoverage(context.Background(), srv.URL, tile, time.Hour)
		if err != nil || string(data) != "cov" {
			t.Fatalf("coverage = %q, %v", data, err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("fetched %d times, want 1 (cached)", calls.Load())
	}
	if err := c.PruneCache(-time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.CoveragePath(tile)); err != nil {
		t.Errorf("prune removed the coverage cache: %v", err)
	}
	// A stale entry is fetched again.
	if _, err := c.FetchCoverage(context.Background(), srv.URL, tile, -time.Second); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("stale coverage not refreshed (calls = %d)", calls.Load())
	}
}
