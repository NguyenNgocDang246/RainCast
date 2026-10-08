// Package himawari fetches Himawari infrared (band 13, 10.4 µm) tiles from
// the Japan Meteorological Agency's public map, for cloud-top features of
// the forecast. Tiles are Web Mercator JPEGs, the same projection as the
// radar, and gray: the brighter, the colder the cloud top (clear sea and
// land are about 16–50). Only the order of the gray values is used, never
// a temperature, so the unpublished color scale does not matter.
//
// The JMA endpoint is not an official API: requests are paced, every tile
// is cached for good, and a missing scan is remembered.
package himawari

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"raincast/internal/geo"
)

const (
	// DefaultBaseURL serves the full-disk scans and their list.
	DefaultBaseURL = "https://www.jma.go.jp/bosai/himawari/data/satimg"
	// Zoom is the tile zoom used: about 4.9 km per pixel at the equator,
	// near the band's own 2 km once parallax and the scan's ten minutes
	// are allowed for.
	Zoom = 5
	// ScanStep is the time between full-disk scans.
	ScanStep = 600
	// RadarZoom is the zoom of the radar mosaics the features line up with.
	RadarZoom = 7
	// zoomShift turns radar pixels into satellite pixels.
	zoomShift = RadarZoom - Zoom

	// timeLayout names a scan by its start time (UTC).
	timeLayout = "20060102150405"
	// recentMissing is how long a missing tile may still be on its way:
	// JMA publishes a scan about 20 minutes after it starts.
	recentMissing = 90 * time.Minute
)

// Covers reports whether Himawari sees (lat, lon) well enough: within
// about 65° of the sub-satellite point at 140.7°E, i.e. 75°E–155°W.
func Covers(lat, lon float64) bool {
	d := math.Mod(lon-140.7+540, 360) - 180
	return math.Abs(lat) <= 60 && math.Abs(d) <= 65
}

// Candidates are the scans a forecast from the radar frame at t may use,
// best first: the one that started 10 minutes before the frame, else 20.
// JMA publishes a scan about 20 minutes after it starts and the radar frame
// reaches the app about 10 minutes after its time, so neither is from the
// future of a live forecast.
func Candidates(t int64) []int64 {
	base := t - t%ScanStep
	return []int64{base - ScanStep, base - 2*ScanStep}
}

// Tile is a satellite tile at Zoom.
type Tile struct{ X, Y int }

// RegionTiles are the tiles under the 3×3 radar mosaic around radar tile
// (tx, ty), with margin satellite pixels to spare on every side.
func RegionTiles(tx, ty, margin int) []Tile {
	ts := geo.TileSize
	x0 := ((tx-1)*ts)>>zoomShift - margin
	x1 := ((tx+2)*ts)>>zoomShift + margin - 1
	y0 := ((ty-1)*ts)>>zoomShift - margin
	y1 := ((ty+2)*ts)>>zoomShift + margin - 1
	n := 1 << Zoom
	var out []Tile
	for y := max(floorDiv(y0, ts), 0); y <= min(floorDiv(y1, ts), n-1); y++ {
		for x := floorDiv(x0, ts); x <= floorDiv(x1, ts); x++ {
			t := Tile{((x % n) + n) % n, y}
			if !containsTile(out, t) {
				out = append(out, t)
			}
		}
	}
	return out
}

func containsTile(ts []Tile, t Tile) bool {
	for _, u := range ts {
		if u == t {
			return true
		}
	}
	return false
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// ErrMissing is a tile JMA does not have (yet).
var ErrMissing = errors.New("himawari: tile missing")

// Stats counts network use.
type Stats struct {
	Requests  int64 `json:"requests"`
	CacheHits int64 `json:"cache_hits"`
	Missing   int64 `json:"missing"`
	Throttled int64 `json:"throttled"` // 429 and 503 answers
	Errors    int64 `json:"errors"`
}

// Client fetches and caches tiles.
type Client struct {
	HTTP      *http.Client
	BaseURL   string
	CacheDir  string // required: tiles are never fetched twice
	Limiter   *rate.Limiter
	Retries   int
	Backoff   time.Duration
	UserAgent string
	now       func() time.Time

	requests, hits, missing, throttled, errs atomic.Int64

	mu       sync.Mutex
	coolTill time.Time // no requests before this, after a 429
}

// New returns a client caching under cacheDir, at perSecond requests.
func New(cacheDir string, perSecond float64) *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 20 * time.Second},
		BaseURL:   DefaultBaseURL,
		CacheDir:  cacheDir,
		Limiter:   rate.NewLimiter(rate.Limit(perSecond), 2),
		Retries:   2,
		Backoff:   2 * time.Second,
		UserAgent: "raincast/1.0 (radar nowcast research)",
		now:       time.Now,
	}
}

// Stats reports network use since the client started.
func (c *Client) Stats() Stats {
	return Stats{Requests: c.requests.Load(), CacheHits: c.hits.Load(), Missing: c.missing.Load(),
		Throttled: c.throttled.Load(), Errors: c.errs.Load()}
}

// ScanName is the JMA name of the scan starting at t (unix s).
func ScanName(t int64) string { return time.Unix(t, 0).UTC().Format(timeLayout) }

// Scans lists the scans JMA currently offers (about the last day and a
// half), oldest first. Older tiles stay downloadable for a few more days.
func (c *Client) Scans(ctx context.Context) ([]int64, error) {
	data, err := c.get(ctx, c.BaseURL+"/targetTimes_fd.json")
	if err != nil {
		return nil, err
	}
	return parseScans(data)
}

func parseScans(data []byte) ([]int64, error) {
	var list []struct {
		Basetime string `json:"basetime"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(list))
	for _, s := range list {
		t, err := time.Parse(timeLayout, s.Basetime)
		if err != nil {
			return nil, fmt.Errorf("himawari: scan %q: %w", s.Basetime, err)
		}
		out = append(out, t.Unix())
	}
	return out, nil
}

func (c *Client) tilePath(scan int64, t Tile) string {
	name := ScanName(scan)
	return filepath.Join(c.CacheDir, "B13", strconv.Itoa(Zoom), name[:8], name, fmt.Sprintf("%d_%d.jpg", t.X, t.Y))
}

// Cached returns the tile from the cache only: ErrMissing when JMA had
// no such tile, os.ErrNotExist when it was never fetched.
func (c *Client) Cached(scan int64, t Tile) ([]byte, error) {
	p := c.tilePath(scan, t)
	data, err := os.ReadFile(p)
	if err == nil {
		return data, nil
	}
	if _, merr := os.Stat(p + ".missing"); merr == nil {
		return nil, ErrMissing
	}
	return nil, err
}

// Fetch returns the tile, from the cache or JMA, caching it for good.
func (c *Client) Fetch(ctx context.Context, scan int64, t Tile) ([]byte, error) {
	data, err := c.Cached(scan, t)
	if err == nil || errors.Is(err, ErrMissing) {
		c.hits.Add(1)
		return data, err
	}
	name := ScanName(scan)
	url := fmt.Sprintf("%s/%s/fd/%s/B13/TBB/%d/%d/%d.jpg", c.BaseURL, name, name, Zoom, t.X, t.Y)
	data, err = c.get(ctx, url)
	p := c.tilePath(scan, t)
	switch {
	case errors.Is(err, ErrMissing):
		// A scan this recent may only be late; an older one never comes.
		if c.now().Sub(time.Unix(scan, 0)) > recentMissing {
			_ = writeFile(p+".missing", nil)
		}
		return nil, err
	case err != nil:
		return nil, err
	}
	if _, derr := jpeg.DecodeConfig(bytes.NewReader(data)); derr != nil {
		return nil, fmt.Errorf("himawari: %s: %w", url, derr)
	}
	if werr := writeFile(p, data); werr != nil {
		return nil, werr
	}
	return data, nil
}

// PrefetchFor caches, for the radar frame at radarTime, the first
// candidate scan JMA has (Candidates) over tiles. It returns the scan, or
// 0 when no candidate has them.
func (c *Client) PrefetchFor(ctx context.Context, radarTime int64, tiles []Tile) (int64, error) {
	for _, scan := range Candidates(radarTime) {
		ok := true
		for _, t := range tiles {
			if _, err := c.Fetch(ctx, scan, t); err != nil {
				if errors.Is(err, ErrMissing) {
					ok = false
					break
				}
				return 0, err
			}
		}
		if ok {
			return scan, nil
		}
	}
	return 0, nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
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

// errThrottled is a 429 or 503 answer.
type errThrottled struct{ after time.Duration }

func (e errThrottled) Error() string { return "himawari: throttled" }

// coolDown is how long to stop after a 429 that names no wait.
const coolDown = 2 * time.Minute

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	var last error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			delay := c.Backoff << (attempt - 1)
			var th errThrottled
			if errors.As(last, &th) {
				delay = max(delay, th.after)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		c.mu.Lock()
		wait := c.coolTill.Sub(c.now())
		c.mu.Unlock()
		if wait > 0 {
			return nil, fmt.Errorf("himawari: cooling down for %s after being throttled", wait.Round(time.Second))
		}
		if c.Limiter != nil {
			if err := c.Limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}
		data, err := c.getOnce(ctx, url)
		if err == nil || errors.Is(err, ErrMissing) {
			return data, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var th errThrottled
		if errors.As(err, &th) {
			c.throttled.Add(1)
			c.mu.Lock()
			c.coolTill = c.now().Add(max(th.after, coolDown))
			c.mu.Unlock()
			return nil, err
		}
		last = err
	}
	c.errs.Add(1)
	return nil, fmt.Errorf("himawari: %s: %w", url, last)
}

func (c *Client) getOnce(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	c.requests.Add(1)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		return data, nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden:
		// S3 answers 403 for a key that does not exist.
		c.missing.Add(1)
		return nil, ErrMissing
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable:
		after := time.Duration(0)
		if secs, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && secs > 0 {
			after = min(time.Duration(secs)*time.Second, 10*time.Minute)
		}
		return nil, errThrottled{after}
	default:
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
}

// PruneCache removes scans older than maxAge.
func (c *Client) PruneCache(maxAge time.Duration) error {
	root := filepath.Join(c.CacheDir, "B13", strconv.Itoa(Zoom))
	days, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cut := c.now().Add(-maxAge)
	for _, d := range days {
		scans, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			continue
		}
		left := len(scans)
		for _, s := range scans {
			t, err := time.Parse(timeLayout, s.Name())
			if err != nil || !t.Before(cut) {
				continue
			}
			if os.RemoveAll(filepath.Join(root, d.Name(), s.Name())) == nil {
				left--
			}
		}
		if left == 0 {
			os.Remove(filepath.Join(root, d.Name()))
		}
	}
	return nil
}

// Frame is one scan's gray values over some tiles, read in radar pixels.
type Frame struct {
	Scan  int64
	tiles map[Tile]*image.Gray
}

// LoadFrame decodes the cached tiles of scan; tiles not cached are left
// out. It returns nil when none is.
func (c *Client) LoadFrame(scan int64, tiles []Tile) *Frame {
	f := &Frame{Scan: scan, tiles: map[Tile]*image.Gray{}}
	for _, t := range tiles {
		data, err := c.Cached(scan, t)
		if err != nil {
			continue
		}
		if g, err := decodeGray(data); err == nil {
			f.tiles[t] = g
		}
	}
	if len(f.tiles) == 0 {
		return nil
	}
	return f
}

// LoadFrameFor loads the first candidate scan for the radar frame at t
// that is cached over tiles; nil when none is.
func (c *Client) LoadFrameFor(t int64, tiles []Tile) *Frame {
	for _, scan := range Candidates(t) {
		if f := c.LoadFrame(scan, tiles); f != nil {
			return f
		}
	}
	return nil
}

func decodeGray(data []byte) (*image.Gray, error) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if g, ok := img.(*image.Gray); ok {
		return g, nil
	}
	b := img.Bounds()
	g := image.NewGray(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, gg, bb, _ := img.At(x, y).RGBA()
			g.Pix[(y-b.Min.Y)*g.Stride+(x-b.Min.X)] = uint8((r + gg + bb) / 3 >> 8)
		}
	}
	return g, nil
}

// At returns the gray value at global satellite pixel (x, y), bilinearly
// interpolated; ok is false off the loaded tiles.
func (f *Frame) At(x, y float64) (float64, bool) {
	x0, y0 := math.Floor(x-0.5), math.Floor(y-0.5)
	tx, ty := x-0.5-x0, y-0.5-y0
	var v [4]float64
	for i, d := range [4][2]float64{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		p, ok := f.pixel(int(x0+d[0]), int(y0+d[1]))
		if !ok {
			return 0, false
		}
		v[i] = p
	}
	return (v[0]*(1-tx)+v[1]*tx)*(1-ty) + (v[2]*(1-tx)+v[3]*tx)*ty, true
}

func (f *Frame) pixel(x, y int) (float64, bool) {
	ts := geo.TileSize
	n := (1 << Zoom) * ts
	if y < 0 || y >= n {
		return 0, false
	}
	x = ((x % n) + n) % n
	g, ok := f.tiles[Tile{x / ts, y / ts}]
	if !ok {
		return 0, false
	}
	return float64(g.Pix[(y%ts)*g.Stride+x%ts]), true
}

// FromRadar converts a global radar pixel (zoom RadarZoom) into a global
// satellite pixel.
func FromRadar(x, y float64) (float64, float64) {
	s := float64(int(1) << zoomShift)
	return x / s, y / s
}

// DefaultMargin is how many satellite pixels (~5 km each) are kept around a
// radar mosaic, for cloud tops upstream of its edge.
const DefaultMargin = 32
