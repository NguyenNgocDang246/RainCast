// Package pipeline answers forecast requests for the web app. It keeps the
// RainViewer frame index fresh and, when someone asks about a place, loads
// the radar around it and runs the forecast model. It downloads nothing in
// the background: collecting data for backtesting is cmd/collect's job.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"raincast/internal/geo"
	"raincast/internal/model"
	"raincast/internal/motion"
	"raincast/internal/nowcast"
	"raincast/internal/radar"
	"raincast/internal/rainviewer"

	"golang.org/x/sync/singleflight"
)

// Station is a named place. The first configured one biases address search.
type Station struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// DefaultStations spread across greater Ho Chi Minh City.
func DefaultStations() []Station {
	return []Station{
		{"thu-duc", "Thủ Đức", 10.8500, 106.7700},
		{"q1", "Quận 1", 10.7769, 106.7009},
		{"tan-binh", "Tân Bình", 10.8015, 106.6527},
		{"binh-tan", "Bình Tân", 10.7652, 106.6039},
		{"q7", "Quận 7", 10.7290, 106.7190},
		{"binh-chanh", "Bình Chánh", 10.6872, 106.5938},
		{"hoc-mon", "Hóc Môn", 10.8863, 106.5922},
		{"cu-chi", "Củ Chi", 10.9733, 106.4932},
		{"di-an", "Dĩ An", 10.9069, 106.7697},
		{"bien-hoa", "Biên Hòa", 10.9574, 106.8427},
	}
}

// Config describes the forecast settings.
type Config struct {
	Stations  []Station // at least one; the first biases address search
	Zoom      int
	Threshold float32 // dBZ counted as rain
	Heavy     float32 // dBZ counted as heavy rain
	// Likely is the dBZ below which rain is only "possible": very light
	// echoes often evaporate before reaching the ground.
	Likely  float32
	Radius  int // pixels around the target whose median echo is used
	Horizon int // minutes
	MaxGap  time.Duration
	// Model is how forecasts are made (model.Default unless chosen
	// otherwise).
	Model model.Model
	// CacheAge is how long downloaded tiles are kept.
	CacheAge time.Duration
}

// DefaultConfig uses DefaultStations.
func DefaultConfig() Config {
	return Config{
		Stations: DefaultStations(), Zoom: rainviewer.MaxZoom,
		Threshold: 20, Likely: 30, Heavy: 40, Radius: 2, Horizon: 60,
		MaxGap: 30 * time.Minute,
		// Tiles are kept 60 days so past frames can be re-scored (cmd/backtest).
		CacheAge: 60 * 24 * time.Hour,
		Model:    model.Default(),
	}
}

// Location is the forecast target.
type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Snapshot is a forecast plus the data used to render it.
type Snapshot struct {
	Station     string    `json:"station,omitempty"` // set for forecasts stored for a station
	Location    Location  `json:"location"`
	FrameTime   time.Time `json:"frame_time"`
	GeneratedAt time.Time `json:"generated_at"`
	Threshold   float32   `json:"threshold_dbz"`
	Likely      float32   `json:"likely_dbz"`
	Heavy       float32   `json:"heavy_dbz"`
	FramesUsed  int       `json:"frames_used"`
	KmPerPx     float64   `json:"km_per_px"`
	Trend       bool      `json:"trend"` // intensity growth/decay applied
	Model       string    `json:"model"` // forecast model name
	nowcast.Result

	Mosaic *radar.Mosaic `json:"-"`
	Field  *motion.Field `json:"-"`
	X, Y   float64       `json:"-"` // target in mosaic pixels
}

// Pipeline keeps the frame index and serves forecasts on demand.
type Pipeline struct {
	cfg    Config
	client *rainviewer.Client
	log    *slog.Logger

	mu      sync.RWMutex
	host    string             // tile host from the latest index
	frames  []rainviewer.Frame // latest index, ascending
	regions map[regionKey]*region
	flight  singleflight.Group

	started  time.Time
	ticks    int
	lastTick time.Time
	lastErr  string
}

// New builds a pipeline. cfg.Stations must not be empty.
func New(cfg Config, client *rainviewer.Client, log *slog.Logger) *Pipeline {
	return &Pipeline{cfg: cfg, client: client, log: log, regions: map[regionKey]*region{}, started: time.Now()}
}

// Primary is the first station, where address search is biased toward.
func (p *Pipeline) Primary() Station { return p.cfg.Stations[0] }

// Ready reports whether the frame index has been loaded.
func (p *Pipeline) Ready() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.frames) >= 2
}

// Status describes the polling loop for the admin page.
type Status struct {
	Location     Location   `json:"location"` // default location
	Stations     []Station  `json:"stations"`
	Started      time.Time  `json:"started"`
	Ticks        int        `json:"ticks"`
	LastTick     *time.Time `json:"last_tick"`
	LastError    string     `json:"last_error"`
	FramesInFeed int        `json:"frames_in_feed"`
	LatestFrame  *time.Time `json:"latest_frame"`
	Regions      int        `json:"cached_regions"`
	Model        string     `json:"model"`
	Trend        bool       `json:"trend"`
}

// Status returns a snapshot of the polling loop.
func (p *Pipeline) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	pr := p.Primary()
	st := Status{
		Location: Location{pr.Lat, pr.Lon}, Stations: p.cfg.Stations,
		Started: p.started.UTC(), Ticks: p.ticks, LastError: p.lastErr,
		FramesInFeed: len(p.frames), Regions: len(p.regions),
		Model: p.cfg.Model.Name, Trend: p.cfg.Model.Trend,
	}
	if !p.lastTick.IsZero() {
		t := p.lastTick.UTC()
		st.LastTick = &t
	}
	if n := len(p.frames); n > 0 {
		t := time.Unix(p.frames[n-1].Time, 0).UTC()
		st.LatestFrame = &t
	}
	return st
}

// Run refreshes the frame index immediately and then every interval until
// ctx is done.
func (p *Pipeline) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		err := p.Tick(ctx)
		if err != nil && ctx.Err() == nil {
			p.log.Error("tick failed", "err", err)
		}
		p.mu.Lock()
		p.ticks++
		p.lastTick = time.Now()
		p.lastErr = ""
		if err != nil {
			p.lastErr = err.Error()
		}
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick reloads the frame index. Radar tiles are only fetched when a
// forecast is asked for.
func (p *Pipeline) Tick(ctx context.Context) error {
	maps, err := p.client.FetchMaps(ctx)
	if err != nil {
		return err
	}
	frames := append([]rainviewer.Frame(nil), maps.Radar.Past...)
	sort.Slice(frames, func(i, j int) bool { return frames[i].Time < frames[j].Time })
	if len(frames) == 0 {
		return fmt.Errorf("pipeline: no radar frames")
	}
	p.mu.Lock()
	p.host, p.frames = maps.Host, frames
	p.mu.Unlock()
	if err := p.client.PruneCache(p.cfg.CacheAge); err != nil {
		p.log.Warn("prune cache", "err", err)
	}
	return nil
}

// loadMosaic fetches and stitches the side×side tiles whose top-left is
// (tx0, ty0). Tiles outside the world (near the poles or the antimeridian)
// are skipped.
func (p *Pipeline) loadMosaic(ctx context.Context, host string, f rainviewer.Frame, tx0, ty0, side int) (*radar.Mosaic, error) {
	n := 1 << p.cfg.Zoom
	var tiles []rainviewer.Tile
	for dy := range side {
		for dx := range side {
			x, y := tx0+dx, ty0+dy
			if x >= 0 && y >= 0 && x < n && y < n {
				tiles = append(tiles, rainviewer.Tile{Z: p.cfg.Zoom, X: x, Y: y})
			}
		}
	}
	raw, err := p.client.FetchTiles(ctx, host, f.Path, tiles)
	if err != nil {
		return nil, err
	}
	byKey := make(map[radar.TileKey][]byte, len(raw))
	for t, data := range raw {
		byKey[radar.TileKey{X: t.X, Y: t.Y}] = data
	}
	return radar.BuildMosaic(byKey, p.cfg.Zoom, tx0, ty0, side, radar.DefaultPalette)
}

// nowcastOptions returns extrapolation settings for a target at latitude lat.
func (p *Pipeline) nowcastOptions(lat float64) nowcast.Options {
	return nowcast.Options{
		Horizon: p.cfg.Horizon, Threshold: p.cfg.Threshold, Heavy: p.cfg.Heavy,
		Radius: p.cfg.Radius, KmPerPx: geo.MetersPerPixel(lat, p.cfg.Zoom) / 1000,
	}
}

// builder estimates motion over history (oldest first), reading frames
// from grid; a pair more than MaxGap apart is a gap.
func (p *Pipeline) builder(history []rainviewer.Frame, grid func(int64) *radar.Mosaic, cache *model.Cache) *model.Builder {
	idx := make(map[int64]int, len(history))
	for i, f := range history {
		idx[f.Time] = i
	}
	return &model.Builder{
		Grid: func(t int64) *radar.Grid {
			if m := grid(t); m != nil {
				return m.Grid
			}
			return nil
		},
		Prev: func(t int64) (int64, bool) {
			i, ok := idx[t]
			if !ok || i == 0 {
				return 0, false
			}
			prev := history[i-1].Time
			return prev, time.Duration(t-prev)*time.Second <= p.cfg.MaxGap
		},
		Cache: cache,
	}
}

// snapshot runs the model for (lat, lon) over a prepared region.
func (p *Pipeline) snapshot(lat, lon float64, r *region) *Snapshot {
	gx, gy := geo.LatLonToPixel(lat, lon, p.cfg.Zoom)
	x, y := r.cur.Local(gx, gy)
	opt := p.nowcastOptions(lat)
	useTrend := p.cfg.Model.Trend && r.prep.HasTrend()
	return &Snapshot{
		Trend:       useTrend,
		Model:       p.cfg.Model.Name,
		Location:    Location{lat, lon},
		FrameTime:   time.Unix(r.frame, 0).UTC(),
		GeneratedAt: time.Now().UTC(),
		Threshold:   p.cfg.Threshold,
		Likely:      p.cfg.Likely,
		Heavy:       p.cfg.Heavy,
		FramesUsed:  r.prep.Used,
		KmPerPx:     opt.KmPerPx,
		Result:      r.prep.Forecast(r.cur.Grid, x, y, opt, useTrend),
		Mosaic:      r.cur, Field: r.prep.Display, X: x, Y: y,
	}
}
