// Package rainviewer fetches the radar frame index and tiles from RainViewer.
package rainviewer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	// DefaultMapsURL lists the available radar frames.
	DefaultMapsURL = "https://api.rainviewer.com/public/weather-maps.json"
	// MaxZoom is the highest zoom the public API serves; above it RainViewer
	// returns a "Zoom Level Not Supported" image with HTTP 200.
	MaxZoom = 7
	// tileSuffix is color scheme / smooth_snow. The public API ignores the
	// scheme (always Universal Blue); smoothing and snow are off so pixel
	// colors match the palette exactly.
	tileSuffix = "/2/0_0.png"
)

// Frame is one radar image set.
type Frame struct {
	Time int64  `json:"time"`
	Path string `json:"path"`
}

// Maps is the weather-maps.json index.
type Maps struct {
	Generated int64  `json:"generated"`
	Host      string `json:"host"`
	Radar     struct {
		Past    []Frame `json:"past"`
		Nowcast []Frame `json:"nowcast"`
	} `json:"radar"`
}

// Client talks to RainViewer.
type Client struct {
	HTTP     *http.Client
	MapsURL  string
	CacheDir string // empty disables the disk cache
	Retries  int
	Backoff  time.Duration
	Parallel int
}

// New returns a client that caches tiles under cacheDir.
func New(cacheDir string) *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 20 * time.Second},
		MapsURL:  DefaultMapsURL,
		CacheDir: cacheDir,
		Retries:  3,
		Backoff:  500 * time.Millisecond,
		Parallel: 4,
	}
}

// FetchMaps returns the current frame index.
func (c *Client) FetchMaps(ctx context.Context) (*Maps, error) {
	data, err := c.get(ctx, c.MapsURL)
	if err != nil {
		return nil, fmt.Errorf("rainviewer: maps: %w", err)
	}
	var m Maps
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("rainviewer: maps: %w", err)
	}
	if m.Host == "" {
		return nil, errors.New("rainviewer: maps: missing host")
	}
	return &m, nil
}

// Tile identifies a tile at a zoom level.
type Tile struct{ Z, X, Y int }

// TileURL builds the URL of a 256 px tile for a frame path.
func TileURL(host, path string, t Tile) string {
	return fmt.Sprintf("%s%s/256/%d/%d/%d%s", host, path, t.Z, t.X, t.Y, tileSuffix)
}

// FetchTile downloads one tile, reading from and writing to the disk cache.
// Frame paths are immutable, so a cached tile never goes stale.
func (c *Client) FetchTile(ctx context.Context, host, path string, t Tile) ([]byte, error) {
	if t.Z > MaxZoom {
		return nil, fmt.Errorf("rainviewer: zoom %d > max %d", t.Z, MaxZoom)
	}
	cached := c.cachePath(path, t)
	if cached != "" {
		if data, err := os.ReadFile(cached); err == nil {
			return data, nil
		}
	}
	data, err := c.get(ctx, TileURL(host, path, t))
	if err != nil {
		return nil, fmt.Errorf("rainviewer: tile %d/%d/%d: %w", t.Z, t.X, t.Y, err)
	}
	if cached != "" {
		if err := writeAtomic(cached, data); err != nil {
			return nil, err
		}
	}
	return data, nil
}

// FetchTiles downloads tiles concurrently, at most c.Parallel at a time.
func (c *Client) FetchTiles(ctx context.Context, host, path string, tiles []Tile) (map[Tile][]byte, error) {
	out := make([][]byte, len(tiles))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(max(c.Parallel, 1))
	for i, t := range tiles {
		g.Go(func() error {
			data, err := c.FetchTile(ctx, host, path, t)
			out[i] = data
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	m := make(map[Tile][]byte, len(tiles))
	for i, t := range tiles {
		m[t] = out[i]
	}
	return m, nil
}

// PruneCache removes cached frames older than maxAge.
func (c *Client) PruneCache(maxAge time.Duration) error {
	if c.CacheDir == "" {
		return nil
	}
	entries, err := os.ReadDir(c.CacheDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !e.IsDir() || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(c.CacheDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) cachePath(path string, t Tile) string {
	if c.CacheDir == "" {
		return ""
	}
	dir := strings.Trim(strings.ReplaceAll(path, "/", "_"), "_")
	return filepath.Join(c.CacheDir, dir, fmt.Sprintf("%d_%d_%d.png", t.Z, t.X, t.Y))
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tile-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// errPermanent marks responses that retrying cannot fix.
type errPermanent struct{ error }

// get fetches url, retrying network errors, 429 and 5xx with exponential backoff.
func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.Backoff << (attempt - 1)):
			}
		}
		data, err := c.getOnce(ctx, url)
		if err == nil {
			return data, nil
		}
		var p errPermanent
		if errors.As(err, &p) || ctx.Err() != nil {
			return nil, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("after %d attempts: %w", c.Retries+1, lastErr)
}

func (c *Client) getOnce(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errPermanent{err}
	}
	req.Header.Set("User-Agent", "raincast/1.0")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		return data, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	default:
		return nil, errPermanent{fmt.Errorf("http %d", resp.StatusCode)}
	}
}
