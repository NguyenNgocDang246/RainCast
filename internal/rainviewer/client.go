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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	// RateLimit is RainViewer's cap: 100 requests per IP per minute. The
	// client stays 10% under it.
	RateLimit = 90
	// CollectRateLimit caps background collection (every radar region,
	// stations included) so interactive requests always have headroom.
	CollectRateLimit = 55

	// CoverageDir holds radar coverage tiles. They are not frame data, so
	// PruneCache leaves them alone.
	CoverageDir = "coverage"
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
	// Limiter paces every network request; CollectLimiter additionally
	// paces requests whose context is marked with Collecting. Nil disables.
	Limiter        Waiter
	CollectLimiter Waiter

	waiting atomic.Int64
	limit   int // requests per minute, for Stats; RateLimit when 0
	mu      sync.Mutex
	recent  []time.Time // network requests in the last minute
}

// New returns a client that caches tiles under cacheDir.
func New(cacheDir string) *Client {
	return &Client{
		HTTP:           &http.Client{Timeout: 20 * time.Second},
		MapsURL:        DefaultMapsURL,
		CacheDir:       cacheDir,
		Retries:        3,
		Backoff:        500 * time.Millisecond,
		Parallel:       8,
		Limiter:        NewWindow(RateLimit, time.Minute),
		CollectLimiter: NewWindow(CollectRateLimit, time.Minute),
	}
}

// SetRateLimit caps this client at perMinute requests. RainViewer's limit
// is per IP, so processes sharing a machine must split it between them;
// the collection sub-limit is dropped, since the whole client now has one
// purpose.
func (c *Client) SetRateLimit(perMinute int) {
	c.Limiter = NewWindow(perMinute, time.Minute)
	c.CollectLimiter = nil
	c.limit = perMinute
}

type collectingKey struct{}

// Collecting marks ctx as background collection, paced by CollectLimiter
// on top of the global limit.
func Collecting(ctx context.Context) context.Context {
	return context.WithValue(ctx, collectingKey{}, true)
}

// RateStats describes recent network use.
type RateStats struct {
	LastMinute int `json:"last_minute"` // requests sent in the last 60 s
	Waiting    int `json:"waiting"`     // requests queued for the limiter
	Limit      int `json:"limit"`
}

// Stats reports recent network use.
func (c *Client) Stats() RateStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trimLocked(time.Now())
	limit := c.limit
	if limit == 0 {
		limit = RateLimit
	}
	return RateStats{LastMinute: len(c.recent), Waiting: int(c.waiting.Load()), Limit: limit}
}

func (c *Client) trimLocked(now time.Time) {
	cut := 0
	for cut < len(c.recent) && now.Sub(c.recent[cut]) >= time.Minute {
		cut++
	}
	c.recent = c.recent[cut:]
}

// wait blocks until the limiters allow one more request.
func (c *Client) wait(ctx context.Context) error {
	c.waiting.Add(1)
	defer c.waiting.Add(-1)
	if ctx.Value(collectingKey{}) != nil && c.CollectLimiter != nil {
		if err := c.CollectLimiter.Wait(ctx); err != nil {
			return err
		}
	}
	if c.Limiter != nil {
		if err := c.Limiter.Wait(ctx); err != nil {
			return err
		}
	}
	now := time.Now()
	c.mu.Lock()
	c.trimLocked(now)
	c.recent = append(c.recent, now)
	c.mu.Unlock()
	return nil
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

// MapTileTemplate is a Leaflet {z}/{x}/{y} URL for drawing a frame on a map:
// smoothed, unlike the tiles the forecast reads.
func MapTileTemplate(host, path string) string {
	return host + path + "/256/{z}/{x}/{y}/2/1_0.png"
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

// CachedTile reads a tile from the disk cache only.
func (c *Client) CachedTile(path string, t Tile) ([]byte, bool) {
	p := c.cachePath(path, t)
	if p == "" {
		return nil, false
	}
	data, err := os.ReadFile(p)
	return data, err == nil
}

// HasCachedTile reports whether a tile is in the disk cache, without
// reading it.
func (c *Client) HasCachedTile(path string, t Tile) bool {
	p := c.cachePath(path, t)
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// CachedCoverage reads a coverage tile from the disk cache only.
func (c *Client) CachedCoverage(t Tile) ([]byte, bool) {
	p := c.CoveragePath(t)
	if p == "" {
		return nil, false
	}
	data, err := os.ReadFile(p)
	return data, err == nil
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

// CoverageURL is the tile showing where RainViewer has radar: transparent
// pixels are covered, opaque black ones are not.
func CoverageURL(host string, t Tile) string {
	return fmt.Sprintf("%s/v2/coverage/0/256/%d/%d/%d/0/0_0.png", host, t.Z, t.X, t.Y)
}

// FetchCoverage returns a coverage tile, from the disk cache when it is
// younger than maxAge. Coverage changes only when radars come and go.
func (c *Client) FetchCoverage(ctx context.Context, host string, t Tile, maxAge time.Duration) ([]byte, error) {
	cached := c.CoveragePath(t)
	if cached != "" {
		if info, err := os.Stat(cached); err == nil && time.Since(info.ModTime()) < maxAge {
			if data, err := os.ReadFile(cached); err == nil {
				return data, nil
			}
		}
	}
	data, err := c.get(ctx, CoverageURL(host, t))
	if err != nil {
		return nil, fmt.Errorf("rainviewer: coverage %d/%d/%d: %w", t.Z, t.X, t.Y, err)
	}
	if cached != "" {
		if err := writeAtomic(cached, data); err != nil {
			return nil, err
		}
	}
	return data, nil
}

// CoveragePath is where a coverage tile is cached, or "" without a cache.
func (c *Client) CoveragePath(t Tile) string {
	if c.CacheDir == "" {
		return ""
	}
	return filepath.Join(c.CacheDir, CoverageDir, fmt.Sprintf("%d_%d_%d.png", t.Z, t.X, t.Y))
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
		if err != nil || !e.IsDir() || e.Name() == CoverageDir || info.ModTime().After(cutoff) {
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
	return filepath.Join(c.FrameDir(path), TileFile(t))
}

// FrameDir is the cache directory holding the tiles of the frame at path
// (empty without a cache).
func (c *Client) FrameDir(path string) string {
	if c.CacheDir == "" {
		return ""
	}
	return filepath.Join(c.CacheDir, strings.Trim(strings.ReplaceAll(path, "/", "_"), "_"))
}

// TileFile is the file name of tile t inside a FrameDir.
func TileFile(t Tile) string { return fmt.Sprintf("%d_%d_%d.png", t.Z, t.X, t.Y) }

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

// errRetryAfter is a 429 or 503 that says how long to wait.
type errRetryAfter struct {
	error
	after time.Duration
}

// get fetches url, retrying network errors, 429 and 5xx with exponential backoff.
func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			delay := c.Backoff << (attempt - 1)
			var ra errRetryAfter
			if errors.As(lastErr, &ra) {
				delay = max(delay, ra.after)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		if err := c.wait(ctx); err != nil {
			return nil, err
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
		err := fmt.Errorf("http %d", resp.StatusCode)
		if secs, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && secs > 0 {
			return nil, errRetryAfter{err, min(time.Duration(secs)*time.Second, 2*time.Minute)}
		}
		return nil, err
	default:
		return nil, errPermanent{fmt.Errorf("http %d", resp.StatusCode)}
	}
}
