package geocode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"
)

// DefaultTileURL is Geoapify's raster map tile host.
const DefaultTileURL = "https://maps.geoapify.com/v1/tile"

// MaxTileZoom is the deepest zoom served; street level is plenty to pick a point.
const MaxTileZoom = 18

// ErrBadTile means the tile coordinates are out of range.
var ErrBadTile = errors.New("geocode: tile out of range")

// maxTileBytes caps one upstream tile.
const maxTileBytes = 2 << 20

// Tile returns a 256 px PNG map tile, from the disk cache in TileDir when
// present and younger than TileAge. Tiles go through the server so the
// browser never sees the key.
func (c *Client) Tile(ctx context.Context, z, x, y int) ([]byte, error) {
	if z < 0 || z > MaxTileZoom || x < 0 || y < 0 || x >= 1<<z || y >= 1<<z {
		return nil, ErrBadTile
	}
	path := ""
	var stale []byte
	if c.TileDir != "" {
		path = filepath.Join(c.TileDir, c.TileStyle, strconv.Itoa(z), strconv.Itoa(x), strconv.Itoa(y)+".png")
		if info, err := os.Stat(path); err == nil {
			if b, err := os.ReadFile(path); err == nil {
				if !c.tileExpired(info.ModTime(), time.Now()) {
					c.stats.tileCacheHits.Add(1)
					return b, nil
				}
				stale = b
			}
		}
	}
	b, err := c.fetchTile(ctx, z, x, y)
	if ctx.Err() != nil {
		return nil, ctx.Err() // the map panned away
	}
	if err != nil {
		c.stats.tileErrors.Add(1)
		if stale != nil {
			return stale, nil // an old map beats no map
		}
		return nil, err
	}
	c.stats.tileFetched.Add(1)
	if path != "" {
		if err := writeAtomic(path, b); err != nil {
			c.logf("tile cache write failed", "err", err)
		}
	}
	return b, nil
}

func (c *Client) tileExpired(mod, now time.Time) bool {
	return c.TileAge > 0 && now.Sub(mod) >= c.TileAge
}

// PruneTiles deletes cached map tiles older than TileAge, along with temp
// files a crashed write left behind, then the oldest tiles until the cache
// fits in TileMaxBytes.
func (c *Client) PruneTiles() error {
	if c.TileDir == "" || (c.TileAge <= 0 && c.TileMaxBytes <= 0) {
		return nil
	}
	type tile struct {
		path string
		size int64
		mod  time.Time
	}
	var kept []tile
	var total int64
	now := time.Now()
	err := filepath.WalkDir(c.TileDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if !c.tileExpired(info.ModTime(), now) {
			kept = append(kept, tile{path, info.Size(), info.ModTime()})
			total += info.Size()
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || c.TileMaxBytes <= 0 || total <= c.TileMaxBytes {
		return err
	}
	slices.SortFunc(kept, func(a, b tile) int { return a.mod.Compare(b.mod) })
	for _, t := range kept {
		if total <= c.TileMaxBytes {
			break
		}
		if err := os.Remove(t.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		total -= t.size
	}
	return nil
}

func (c *Client) fetchTile(ctx context.Context, z, x, y int) ([]byte, error) {
	if c.Key == "" {
		return nil, ErrNoKey
	}
	if c.pause.active(time.Now()) {
		return nil, errRateLimited
	}
	// Tiles share the key's request quota with geocoding.
	if err := c.rate.wait(ctx); err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/%s/%d/%d/%d.png?apiKey=%s", c.TileURL, c.TileStyle, z, x, y, url.QueryEscape(c.Key))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("geoapify tile: %w", stripURL(err))
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("geoapify tile: %w", stripURL(err))
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests:
		c.pause.extend(time.Now(), 2*time.Second)
		return nil, errRateLimited
	default:
		return nil, fmt.Errorf("geoapify tile: http %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxTileBytes))
	if err != nil {
		return nil, fmt.Errorf("geoapify tile: %w", err)
	}
	return b, nil
}

// writeAtomic writes via a temp file so a reader never sees half a tile.
func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tile-*")
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(f.Name())
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		os.Remove(f.Name())
		return err
	}
	return nil
}
