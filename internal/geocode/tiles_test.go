package geocode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestTileCachesOnDisk(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/osm-liberty/5/24/14.png" || r.URL.Query().Get("apiKey") != "k" {
			t.Errorf("url = %s", r.URL)
		}
		w.Write([]byte("png"))
	}))
	defer srv.Close()
	c := New("k", "ua", "")
	c.TileURL = srv.URL
	c.TileDir = t.TempDir()

	for range 2 {
		b, err := c.Tile(context.Background(), 5, 24, 14)
		if err != nil || string(b) != "png" {
			t.Fatalf("%q %v", b, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (cached)", calls.Load())
	}
	if st := c.Stats(); st.TileFetched != 1 || st.TileCacheHits != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestTileExpires(t *testing.T) {
	var calls atomic.Int32
	fail := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte("png"))
	}))
	defer srv.Close()
	c := New("k", "ua", "")
	c.TileURL = srv.URL
	c.TileDir = t.TempDir()
	path := filepath.Join(c.TileDir, c.TileStyle, "5", "24", "14.png")
	age := func() {
		old := time.Now().Add(-c.TileAge - time.Minute)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := c.Tile(context.Background(), 5, 24, 14); err != nil {
		t.Fatal(err)
	}
	age()
	if _, err := c.Tile(context.Background(), 5, 24, 14); err != nil || calls.Load() != 2 {
		t.Fatalf("calls = %d, err %v; an expired tile should be fetched again", calls.Load(), err)
	}

	// An expired tile is still served when the upstream fails.
	age()
	fail.Store(true)
	if b, err := c.Tile(context.Background(), 5, 24, 14); err != nil || string(b) != "png" {
		t.Fatalf("%q %v, want the stale tile", b, err)
	}

	if err := c.PruneTiles(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired tile still there: %v", err)
	}
}

func TestPruneTilesKeepsFresh(t *testing.T) {
	c := New("k", "ua", "")
	c.TileDir = t.TempDir()
	path := filepath.Join(c.TileDir, "s", "1", "0", "0.png")
	if err := writeAtomic(path, []byte("png")); err != nil {
		t.Fatal(err)
	}
	if err := c.PruneTiles(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fresh tile removed: %v", err)
	}
	c.TileDir = filepath.Join(c.TileDir, "missing")
	if err := c.PruneTiles(); err != nil {
		t.Fatalf("missing dir: %v", err)
	}
}

func TestTileRange(t *testing.T) {
	c := New("k", "ua", "")
	c.TileURL = "http://127.0.0.1:1" // any request would fail differently
	for _, zxy := range [][3]int{{-1, 0, 0}, {MaxTileZoom + 1, 0, 0}, {2, 4, 0}, {2, 0, 4}, {2, -1, 0}} {
		if _, err := c.Tile(context.Background(), zxy[0], zxy[1], zxy[2]); !errors.Is(err, ErrBadTile) {
			t.Errorf("%v: %v", zxy, err)
		}
	}
}

func TestPruneTilesSizeCap(t *testing.T) {
	c := New("k", "ua", "")
	c.TileDir = t.TempDir()
	c.TileMaxBytes = 25
	now := time.Now()
	var paths []string
	for i := range 4 { // 10 bytes each, oldest first
		path := filepath.Join(c.TileDir, "s", "1", "0", strconv.Itoa(i)+".png")
		if err := writeAtomic(path, []byte("0123456789")); err != nil {
			t.Fatal(err)
		}
		mod := now.Add(time.Duration(i-4) * time.Minute)
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	if err := c.PruneTiles(); err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		_, err := os.Stat(path)
		if gone := errors.Is(err, os.ErrNotExist); gone != (i < 2) {
			t.Errorf("tile %d: gone = %v, want the two oldest removed", i, gone)
		}
	}
}
