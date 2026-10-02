// Package collect stores radar frames from many places at once so the
// backtest has enough independent rain to compare forecast methods within a
// day. A global low-zoom scout finds where it is raining under RainViewer
// radar coverage, and the collector caches the 3×3 z7 tiles around the
// rainiest spots, the same mosaic the pipeline uses for a station.
package collect

import (
	"context"
	"log/slog"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"raincast/internal/geo"
	"raincast/internal/radar"
	"raincast/internal/rainviewer"
	"raincast/internal/store"
)

// Config tunes the collector.
type Config struct {
	Regions    int           // active regions; 0 disables collection
	ScoutEvery time.Duration // how often the global scout runs
	// Hold keeps a region this long after it last rained, so every rainy
	// frame has the history and future frames the backtest needs.
	Hold    time.Duration
	RainDBZ float32 // echo counted as rain
	// MinRain is the share of the center tile that must rain for a frame to
	// count as rainy.
	MinRain float64
	// CoverCenter and CoverMosaic are the radar coverage required of the
	// center tile and of the whole 3×3 mosaic.
	CoverCenter, CoverMosaic float64
	CoverageAge              time.Duration // how long coverage tiles are trusted
}

// DefaultConfig keeps 30 regions: about 30 rain events a day, within
// RainViewer's request limit.
func DefaultConfig() Config {
	return Config{
		Regions: 30, ScoutEvery: 20 * time.Minute, Hold: 6 * time.Hour,
		RainDBZ: 20, MinRain: 0.02, CoverCenter: 0.8, CoverMosaic: 0.5,
		CoverageAge: 7 * 24 * time.Hour,
	}
}

// Fetcher is the part of rainviewer.Client the collector uses.
type Fetcher interface {
	FetchTile(ctx context.Context, host, path string, t rainviewer.Tile) ([]byte, error)
	FetchTiles(ctx context.Context, host, path string, tiles []rainviewer.Tile) (map[rainviewer.Tile][]byte, error)
	FetchCoverage(ctx context.Context, host string, t rainviewer.Tile, maxAge time.Duration) ([]byte, error)
}

// Store is the part of store.Store the collector uses.
type Store interface {
	TouchRegion(ctx context.Context, r store.Region, now int64) error
	RecordFrameOnly(ctx context.Context, t int64, path string, now int64) error
}

// Feed returns the tile host and the current frame index, oldest first.
type Feed func() (host string, frames []rainviewer.Frame)

const (
	zoom      = rainviewer.MaxZoom
	scoutZoom = 2
	// cellPx is the size of one z7 tile in scout pixels.
	cellPx = geo.TileSize >> (zoom - scoutZoom)
	// rejectFor keeps a region that failed coverage or stayed dry from
	// being picked again right away.
	rejectFor = 6 * time.Hour
	// historyKeep is how long per-region rain history is kept for stats.
	historyKeep = 24 * time.Hour
	// eventFrames is how many consecutive rainy frames make a rain event.
	eventFrames = 6
)

type key struct{ X, Y int }

// Region is an active collection area.
type Region struct {
	TileX     int       `json:"tile_x"`
	TileY     int       `json:"tile_y"`
	Lat       float64   `json:"lat"`
	Lon       float64   `json:"lon"`
	Climate   string    `json:"climate"`
	Score     float64   `json:"score"` // scout rain share when picked
	Activated time.Time `json:"activated"`
	LastRain  time.Time `json:"last_rain"` // zero until it rains at z7

	checked   int   // newest frames inspected at z7
	lastFrame int64 // newest frame inspected
	cov       *radar.Coverage
}

type sample struct {
	t    int64
	rain bool
}

type candidate struct {
	key
	score   float64
	climate string
}

// Collector keeps the rainiest regions' tiles in the cache.
type Collector struct {
	cfg    Config
	client Fetcher
	store  Store
	feed   Feed
	log    *slog.Logger
	now    func() time.Time

	mu         sync.Mutex
	active     map[key]*Region
	rejected   map[key]time.Time
	candidates []candidate
	lastScout  time.Time
	history    map[key][]sample // newest-frame rain per region, last 24 h
	lastErr    string
	// budget bounds how long one tick spends backfilling older frames, so
	// scouting and new frames are never held up; 0 means no bound.
	budget time.Duration
}

// New returns a collector.
func New(cfg Config, client Fetcher, st Store, feed Feed, log *slog.Logger) *Collector {
	return &Collector{
		cfg: cfg, client: client, store: st, feed: feed, log: log, now: time.Now,
		active: map[key]*Region{}, rejected: map[key]time.Time{}, history: map[key][]sample{},
	}
}

// feedRetry is how soon to look again while the frame index has not been
// loaded yet.
const feedRetry = 10 * time.Second

// Run collects every interval until ctx is done.
func (c *Collector) Run(ctx context.Context, interval time.Duration) {
	if c.cfg.Regions <= 0 {
		return
	}
	for {
		if _, frames := c.feed(); len(frames) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(feedRetry):
				continue
			}
		}
		break
	}
	c.budget = interval
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		err := c.Tick(ctx)
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		c.lastErr = ""
		if err != nil {
			c.lastErr = err.Error()
		}
		c.mu.Unlock()
		if err != nil {
			c.log.Warn("collect: tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick scouts when due, tops up the active regions and caches every frame
// in the feed for each of them.
func (c *Collector) Tick(ctx context.Context) error {
	ctx = rainviewer.Collecting(ctx)
	host, frames := c.feed()
	if len(frames) == 0 {
		return nil // the pipeline has not loaded the index yet
	}
	now := c.now()
	c.mu.Lock()
	due := now.Sub(c.lastScout) >= c.cfg.ScoutEvery
	c.mu.Unlock()
	if due || c.needCandidates() {
		if err := c.scout(ctx, host, frames[len(frames)-1]); err != nil {
			return err
		}
		c.mu.Lock()
		c.lastScout = now
		c.mu.Unlock()
	}
	c.fill(ctx, host)
	var deadline time.Time
	if c.budget > 0 {
		deadline = time.Now().Add(c.budget)
	}
	c.collect(ctx, host, frames, deadline)
	c.expire(now)
	return ctx.Err()
}

func (c *Collector) needCandidates() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active) < c.cfg.Regions && len(c.candidates) == 0
}

// scout ranks every well-covered z7 tile by its share of rain in the z2
// view of frame f: 16 radar tiles and 16 (cached) coverage tiles show the
// whole world.
func (c *Collector) scout(ctx context.Context, host string, f rainviewer.Frame) error {
	n := 1 << scoutZoom
	var cands []candidate
	for ty := range n {
		for tx := range n {
			t := rainviewer.Tile{Z: scoutZoom, X: tx, Y: ty}
			covData, err := c.client.FetchCoverage(ctx, host, t, 24*time.Hour)
			if err != nil {
				return err
			}
			cov, err := radar.DecodeCoverage(covData)
			if err != nil {
				return err
			}
			if cov.Fraction(0, 0, cov.W, cov.H) == 0 {
				continue // no radar anywhere in this tile: skip the download
			}
			data, err := c.client.FetchTile(ctx, host, f.Path, t)
			if err != nil {
				return err
			}
			g, err := radar.Decode(data, radar.DefaultPalette)
			if err != nil {
				return err
			}
			cands = append(cands, rankCells(g, cov, tx, ty, c.cfg)...)
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	c.mu.Lock()
	c.candidates = cands
	c.mu.Unlock()
	c.log.Info("collect: scouted", "rainy_tiles", len(cands))
	return nil
}

// rankCells scores the z7 tiles inside scout tile (tx, ty).
func rankCells(g *radar.Grid, cov *radar.Coverage, tx, ty int, cfg Config) []candidate {
	per := geo.TileSize / cellPx
	var out []candidate
	for cy := range per {
		for cx := range per {
			x0, y0 := cx*cellPx, cy*cellPx
			if cov.Fraction(x0, y0, cellPx, cellPx) < cfg.CoverCenter {
				continue
			}
			score := radar.RainFraction(g, cov, x0, y0, cellPx, cellPx, cfg.RainDBZ)
			if score < cfg.MinRain {
				continue
			}
			k := key{tx*per + cx, ty*per + cy}
			lat, _ := center(k)
			out = append(out, candidate{key: k, score: score, climate: Climate(lat)})
		}
	}
	return out
}

// Climate groups latitudes so results can be compared with Ho Chi Minh
// City's tropical rain.
func Climate(lat float64) string {
	switch a := math.Abs(lat); {
	case a < 23.5:
		return "tropical"
	case a < 35:
		return "subtropical"
	default:
		return "midlat"
	}
}

func center(k key) (lat, lon float64) {
	return geo.PixelToLatLon((float64(k.X)+0.5)*geo.TileSize, (float64(k.Y)+0.5)*geo.TileSize, zoom)
}

// overlaps reports whether the 3×3 mosaics around a and b share a tile.
func overlaps(a, b key) bool {
	return max(abs(a.X-b.X), abs(a.Y-b.Y)) < 3
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// fill activates candidates until Regions are active: rainiest first, about
// a third per climate group while each has candidates, no overlapping
// mosaics, and only where radar covers the mosaic.
func (c *Collector) fill(ctx context.Context, host string) {
	now := c.now()
	quota := (c.cfg.Regions + 2) / 3
	for pass := range 2 {
		for {
			c.mu.Lock()
			if len(c.active) >= c.cfg.Regions {
				c.mu.Unlock()
				return
			}
			perClimate := map[string]int{}
			for _, r := range c.active {
				perClimate[r.Climate]++
			}
			i := slices.IndexFunc(c.candidates, func(cd candidate) bool {
				return (pass == 1 || perClimate[cd.climate] < quota) && c.eligibleLocked(cd.key, now)
			})
			if i < 0 {
				c.mu.Unlock()
				break
			}
			cd := c.candidates[i]
			c.candidates = slices.Delete(c.candidates, i, i+1)
			c.mu.Unlock()

			cov, ok, err := c.checkCoverage(ctx, host, cd.key)
			if err != nil {
				if ctx.Err() == nil {
					c.log.Warn("collect: coverage", "tile", cd.key, "err", err)
				}
				return
			}
			c.mu.Lock()
			if !ok {
				c.rejected[cd.key] = now
				c.mu.Unlock()
				continue
			}
			lat, lon := center(cd.key)
			c.active[cd.key] = &Region{TileX: cd.X, TileY: cd.Y, Lat: lat, Lon: lon, Climate: cd.climate,
				Score: cd.score, Activated: now, cov: cov}
			c.mu.Unlock()
			c.log.Info("collect: region on", "tile", cd.key, "lat", round1(lat), "lon", round1(lon),
				"climate", cd.climate, "rain", round1(cd.score*100))
		}
	}
}

func (c *Collector) eligibleLocked(k key, now time.Time) bool {
	if t, ok := c.rejected[k]; ok && now.Sub(t) < rejectFor {
		return false
	}
	for a := range c.active {
		if overlaps(a, k) {
			return false
		}
	}
	return true
}

// checkCoverage loads the z7 coverage of k's mosaic and checks it.
func (c *Collector) checkCoverage(ctx context.Context, host string, k key) (*radar.Coverage, bool, error) {
	tiles := map[radar.TileKey][]byte{}
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			t := rainviewer.Tile{Z: zoom, X: k.X + dx, Y: k.Y + dy}
			data, err := c.client.FetchCoverage(ctx, host, t, c.cfg.CoverageAge)
			if err != nil {
				return nil, false, err
			}
			tiles[radar.TileKey{X: t.X, Y: t.Y}] = data
		}
	}
	cov, err := radar.BuildCoverageMosaic(tiles, k.X-1, k.Y-1, 3)
	if err != nil {
		return nil, false, err
	}
	ts := geo.TileSize
	ok := cov.Fraction(ts, ts, ts, ts) >= c.cfg.CoverCenter && cov.Fraction(0, 0, 3*ts, 3*ts) >= c.cfg.CoverMosaic
	return cov, ok, nil
}

// collect caches every frame in the feed for every active region, newest
// frame first so a new region's backfill never delays current data. Older
// frames stop at deadline (when set); the next tick resumes them, and tiles
// already cached cost nothing.
func (c *Collector) collect(ctx context.Context, host string, frames []rainviewer.Frame, deadline time.Time) {
	c.mu.Lock()
	regions := make([]*Region, 0, len(c.active))
	for _, r := range c.active {
		regions = append(regions, r)
	}
	c.mu.Unlock()
	now := c.now()
	newest := frames[len(frames)-1]
	for i := len(frames) - 1; i >= 0; i-- {
		if i < len(frames)-1 && !deadline.IsZero() && time.Now().After(deadline) {
			return
		}
		f := frames[i]
		recorded := false
		for _, r := range regions {
			if ctx.Err() != nil {
				return
			}
			raw, err := c.client.FetchTiles(ctx, host, f.Path, mosaicTiles(r.TileX, r.TileY))
			if err != nil {
				if ctx.Err() == nil {
					c.log.Warn("collect: frame skipped", "tile", key{r.TileX, r.TileY}, "time", f.Time, "err", err)
				}
				continue
			}
			if !recorded {
				if err := c.store.RecordFrameOnly(ctx, f.Time, f.Path, now.Unix()); err != nil {
					c.log.Warn("collect: record frame", "err", err)
				}
				recorded = true
			}
			if err := c.store.TouchRegion(ctx, store.Region{TileX: r.TileX, TileY: r.TileY, Lat: r.Lat, Lon: r.Lon,
				Climate: r.Climate}, now.Unix()); err != nil {
				c.log.Warn("collect: touch region", "err", err)
			}
			if f.Time == newest.Time && r.lastFrame != f.Time {
				c.inspect(r, raw, f.Time)
			}
		}
	}
}

func mosaicTiles(x, y int) []rainviewer.Tile {
	tiles := make([]rainviewer.Tile, 0, 9)
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			tiles = append(tiles, rainviewer.Tile{Z: zoom, X: x + dx, Y: y + dy})
		}
	}
	return tiles
}

// inspect records whether the center tile of the newest frame rains.
func (c *Collector) inspect(r *Region, raw map[rainviewer.Tile][]byte, t int64) {
	byKey := make(map[radar.TileKey][]byte, len(raw))
	for tile, d := range raw {
		byKey[radar.TileKey{X: tile.X, Y: tile.Y}] = d
	}
	m, err := radar.BuildMosaic(byKey, zoom, r.TileX-1, r.TileY-1, 3, radar.DefaultPalette)
	if err != nil {
		c.log.Warn("collect: decode", "tile", key{r.TileX, r.TileY}, "err", err)
		return
	}
	ts := geo.TileSize
	rain := radar.RainFraction(m.Grid, r.cov, ts, ts, ts, ts, c.cfg.RainDBZ) >= c.cfg.MinRain
	c.mu.Lock()
	defer c.mu.Unlock()
	r.checked++
	r.lastFrame = t
	if rain {
		r.LastRain = time.Unix(t, 0)
	}
	k := key{r.TileX, r.TileY}
	h := append(c.history[k], sample{t, rain})
	cut := 0
	for cut < len(h) && t-h[cut].t > int64(historyKeep.Seconds()) {
		cut++
	}
	c.history[k] = h[cut:]
}

// expire drops regions that stayed dry for two frames after being picked,
// or stopped raining more than Hold ago.
func (c *Collector) expire(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, r := range c.active {
		dry := r.LastRain.IsZero() && r.checked >= 2
		done := !r.LastRain.IsZero() && now.Sub(r.LastRain) > c.cfg.Hold
		if dry || done {
			delete(c.active, k)
			c.rejected[k] = now
			c.log.Info("collect: region off", "tile", k, "never_rained", dry)
		}
	}
	for k, s := range c.history {
		if len(s) == 0 || now.Unix()-s[len(s)-1].t > int64(historyKeep.Seconds()) {
			delete(c.history, k)
		}
	}
	for k, t := range c.rejected {
		if now.Sub(t) >= rejectFor {
			delete(c.rejected, k)
		}
	}
}

// Status describes the collector for the admin page.
type Status struct {
	Enabled    bool      `json:"enabled"`
	Target     int       `json:"target"`
	Active     []Region  `json:"active"`
	Candidates int       `json:"candidates"`
	LastScout  time.Time `json:"last_scout"`
	// Frames24h and Rainy24h count newest-frame checks over the last day;
	// Events24h counts runs of at least an hour of rain.
	Frames24h int    `json:"frames_24h"`
	Rainy24h  int    `json:"rainy_24h"`
	Events24h int    `json:"events_24h"`
	LastError string `json:"last_error"`
}

// Status returns the collector's state.
func (c *Collector) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Status{Enabled: c.cfg.Regions > 0, Target: c.cfg.Regions, Candidates: len(c.candidates),
		LastScout: c.lastScout, LastError: c.lastErr, Active: []Region{}}
	for _, r := range c.active {
		st.Active = append(st.Active, *r)
	}
	sort.Slice(st.Active, func(i, j int) bool { return st.Active[i].Activated.Before(st.Active[j].Activated) })
	for _, h := range c.history {
		run := 0
		for _, s := range h {
			st.Frames24h++
			if !s.rain {
				run = 0
				continue
			}
			st.Rainy24h++
			run++
			if run == eventFrames {
				st.Events24h++
			}
		}
	}
	return st
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
