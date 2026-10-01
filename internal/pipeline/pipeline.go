// Package pipeline polls RainViewer and turns each new radar frame into
// stored, later-verified forecasts for a set of fixed stations.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"raincast/internal/geo"
	"raincast/internal/motion"
	"raincast/internal/nowcast"
	"raincast/internal/radar"
	"raincast/internal/rainviewer"
	"raincast/internal/store"

	"golang.org/x/sync/singleflight"
)

// Station is a fixed location forecast and verified on every frame. They
// exist to collect data for tuning the model.
type Station struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// DefaultStations spread across greater Ho Chi Minh City. All fall in the
// same z7 radar tile, so they share one mosaic and motion field per frame.
// The first is the primary station, served when no location is given.
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

// Config describes the stations and forecast settings.
type Config struct {
	Stations  []Station // at least one; the first is primary
	Zoom      int
	Threshold float32 // dBZ counted as rain
	Heavy     float32 // dBZ counted as heavy rain
	// Likely is the dBZ below which rain is only "possible": very light
	// echoes often evaporate before reaching the ground.
	Likely  float32
	Radius  int   // pixels around the target whose median echo is used
	Horizon int   // minutes
	Leads   []int // lead times stored for verification, in minutes
	MaxGap  time.Duration
	// MotionPairs is how many consecutive frame pairs motion is averaged
	// over, newest weighted most.
	MotionPairs int
	// UseTrend serves forecasts with intensity growth/decay. Either way both
	// versions are stored for every station so they can be compared.
	UseTrend bool
	CacheAge time.Duration
}

// DefaultConfig uses DefaultStations.
func DefaultConfig() Config {
	return Config{
		Stations: DefaultStations(), Zoom: rainviewer.MaxZoom,
		Threshold: 20, Likely: 30, Heavy: 40, Radius: 2, Horizon: 60,
		Leads:  []int{10, 20, 30, 40, 50, 60},
		MaxGap: 30 * time.Minute,
		// Tiles are kept a week so past frames can be re-scored (cmd/backtest).
		CacheAge:    7 * 24 * time.Hour,
		MotionPairs: 4,
	}
}

// Location is the forecast target.
type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Snapshot is a forecast plus the data used to render it.
type Snapshot struct {
	Station     string    `json:"station,omitempty"` // set for station forecasts
	Location    Location  `json:"location"`
	FrameTime   time.Time `json:"frame_time"`
	GeneratedAt time.Time `json:"generated_at"`
	Threshold   float32   `json:"threshold_dbz"`
	Likely      float32   `json:"likely_dbz"`
	Heavy       float32   `json:"heavy_dbz"`
	FramesUsed  int       `json:"frames_used"`
	KmPerPx     float64   `json:"km_per_px"`
	Trend       bool      `json:"trend"` // intensity growth/decay applied
	nowcast.Result

	Mosaic *radar.Mosaic `json:"-"`
	Field  *motion.Field `json:"-"`
	X, Y   float64       `json:"-"` // target in mosaic pixels
}

// tile is the center tile of a 3×3 mosaic.
type tile struct{ X, Y int }

// homeRegion is a mosaic area that contains one or more stations.
type homeRegion struct {
	center   tile
	stations []Station
	grids    map[int64]*radar.Mosaic // by frame time
	pairs    map[int64]*motion.Field // motion of the pair ending at a frame
}

// Pipeline owns the polling loop.
type Pipeline struct {
	cfg    Config
	client *rainviewer.Client
	store  *store.Store
	log    *slog.Logger

	homes []*homeRegion // only touched by the polling goroutine

	mu      sync.RWMutex
	latest  map[string]*Snapshot // newest forecast per station
	host    string               // tile host from the latest index
	frames  []rainviewer.Frame   // latest index, ascending
	regions map[regionKey]*region
	flight  singleflight.Group

	started  time.Time
	ticks    int
	lastTick time.Time
	lastErr  string
}

// New builds a pipeline. cfg.Stations must not be empty.
func New(cfg Config, client *rainviewer.Client, st *store.Store, log *slog.Logger) *Pipeline {
	p := &Pipeline{
		cfg: cfg, client: client, store: st, log: log,
		latest:  map[string]*Snapshot{},
		regions: map[regionKey]*region{},
		started: time.Now(),
	}
	byTile := map[tile]*homeRegion{}
	for _, s := range cfg.Stations {
		t := p.tileOf(s.Lat, s.Lon)
		h, ok := byTile[t]
		if !ok {
			h = &homeRegion{center: t, grids: map[int64]*radar.Mosaic{}, pairs: map[int64]*motion.Field{}}
			byTile[t] = h
			p.homes = append(p.homes, h)
		}
		h.stations = append(h.stations, s)
	}
	return p
}

func (p *Pipeline) tileOf(lat, lon float64) tile {
	x, y := geo.TileOf(geo.LatLonToPixel(lat, lon, p.cfg.Zoom))
	return tile{x, y}
}

// Primary is the first configured station.
func (p *Pipeline) Primary() Station { return p.cfg.Stations[0] }

// Latest returns the primary station's newest forecast, or nil before the
// first tick.
func (p *Pipeline) Latest() *Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.latest[p.Primary().ID]
}

// Status describes the polling loop for the admin page.
type Status struct {
	Location     Location   `json:"location"` // primary station
	Stations     []Station  `json:"stations"`
	Started      time.Time  `json:"started"`
	Ticks        int        `json:"ticks"`
	LastTick     *time.Time `json:"last_tick"`
	LastError    string     `json:"last_error"`
	FramesInFeed int        `json:"frames_in_feed"`
	LatestFrame  *time.Time `json:"latest_frame"`
	Regions      int        `json:"cached_regions"`
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

// Run ticks immediately and then every interval until ctx is done.
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

// Tick processes every frame in the index that has not been handled yet.
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

	keep := map[int64]bool{}
	for i, f := range frames {
		keep[f.Time] = true
		for _, h := range p.homes {
			if _, ok := h.grids[f.Time]; ok {
				continue
			}
			m, err := p.loadMosaic(ctx, maps.Host, f, h.center.X-1, h.center.Y-1)
			if err != nil {
				// One broken frame should not block the rest.
				p.log.Warn("frame skipped", "time", f.Time, "err", err)
				continue
			}
			h.grids[f.Time] = m
		}
		if err := p.processFrame(ctx, frames[:i+1], i == len(frames)-1); err != nil {
			return err
		}
	}
	for _, h := range p.homes {
		for t := range h.grids {
			if !keep[t] {
				delete(h.grids, t)
				delete(h.pairs, t)
			}
		}
	}
	if err := p.client.PruneCache(p.cfg.CacheAge); err != nil {
		p.log.Warn("prune cache", "err", err)
	}
	return nil
}

// loadMosaic fetches and stitches the 3×3 tiles whose top-left is (tx0, ty0).
// Tiles outside the world (near the poles or the antimeridian) are skipped.
func (p *Pipeline) loadMosaic(ctx context.Context, host string, f rainviewer.Frame, tx0, ty0 int) (*radar.Mosaic, error) {
	n := 1 << p.cfg.Zoom
	var tiles []rainviewer.Tile
	for dy := range 3 {
		for dx := range 3 {
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
	return radar.BuildMosaic(byKey, p.cfg.Zoom, tx0, ty0, 3, radar.DefaultPalette)
}

// nowcastOptions returns extrapolation settings for a target at latitude lat.
func (p *Pipeline) nowcastOptions(lat float64) nowcast.Options {
	return nowcast.Options{
		Horizon: p.cfg.Horizon, Threshold: p.cfg.Threshold, Heavy: p.cfg.Heavy,
		Radius: p.cfg.Radius, KmPerPx: geo.MetersPerPixel(lat, p.cfg.Zoom) / 1000,
	}
}

// trendSpan is how far back intensity change is measured.
const trendSpan = 20 * time.Minute

// trendFor measures how echoes in frame t grew or decayed since the frame
// trendSpan earlier (or 10 minutes earlier when that one is missing).
func (p *Pipeline) trendFor(grid func(int64) *radar.Mosaic, t int64, field *motion.Field) *motion.Trend {
	cur := grid(t)
	if cur == nil || field == nil {
		return nil
	}
	for _, span := range []time.Duration{trendSpan, 10 * time.Minute} {
		if prev := grid(t - int64(span.Seconds())); prev != nil {
			return motion.EstimateTrend(prev.Grid, cur.Grid, field, span.Minutes(), p.cfg.Threshold-5)
		}
	}
	return nil
}

// trendResult runs the nowcast with the region's intensity trend, or
// returns nil when the region has none.
func (p *Pipeline) trendResult(lat, lon float64, r *region) *nowcast.Result {
	if r.trend == nil {
		return nil
	}
	x, y := r.cur.Local(geo.LatLonToPixel(lat, lon, p.cfg.Zoom))
	opt := p.nowcastOptions(lat)
	opt.Trend = r.trend
	res := nowcast.Forecast(r.cur.Grid, r.field, x, y, opt)
	return &res
}

// snapshot runs the nowcast for (lat, lon) over a prepared region.
func (p *Pipeline) snapshot(lat, lon float64, r *region) *Snapshot {
	gx, gy := geo.LatLonToPixel(lat, lon, p.cfg.Zoom)
	x, y := r.cur.Local(gx, gy)
	opt := p.nowcastOptions(lat)
	useTrend := p.cfg.UseTrend && r.trend != nil
	if useTrend {
		opt.Trend = r.trend
	}
	return &Snapshot{
		Trend:       useTrend,
		Location:    Location{lat, lon},
		FrameTime:   time.Unix(r.frame, 0).UTC(),
		GeneratedAt: time.Now().UTC(),
		Threshold:   p.cfg.Threshold,
		Likely:      p.cfg.Likely,
		Heavy:       p.cfg.Heavy,
		FramesUsed:  r.used,
		KmPerPx:     opt.KmPerPx,
		Result:      nowcast.Forecast(r.cur.Grid, r.field, x, y, opt),
		Mosaic:      r.cur, Field: r.field, X: x, Y: y,
	}
}

// processFrame records every station's observation for the last frame in
// history and issues their forecasts from it. Forecasts are always
// recomputed for the newest frame so snapshots survive restarts.
func (p *Pipeline) processFrame(ctx context.Context, history []rainviewer.Frame, newest bool) error {
	f := history[len(history)-1]

	// Observe per station, so a station added later still gets observations
	// for frames already in the feed.
	observed, err := p.store.ObservedStations(ctx, f.Time)
	if err != nil {
		return err
	}
	obs := map[string]float64{}
	for _, h := range p.homes {
		cur := h.grids[f.Time]
		if cur == nil {
			continue
		}
		for _, s := range h.stations {
			if observed[s.ID] {
				continue
			}
			x, y := cur.Local(geo.LatLonToPixel(s.Lat, s.Lon, p.cfg.Zoom))
			obs[s.ID] = float64(cur.MedianInRadius(x, y, p.cfg.Radius))
		}
	}
	if len(obs) > 0 {
		n, err := p.store.RecordFrame(ctx, f.Time, f.Path, obs, time.Now().Unix())
		if err != nil {
			return err
		}
		p.log.Info("frame recorded", "time", time.Unix(f.Time, 0).Format(time.TimeOnly),
			"stations", len(obs), "verified", n)
	}

	issued := 0
	for _, h := range p.homes {
		cur := h.grids[f.Time]
		if cur == nil {
			continue
		}
		pending := map[string]bool{}
		for _, s := range h.stations {
			done, err := p.store.HasIssue(ctx, s.ID, f.Time)
			if err != nil {
				return err
			}
			pending[s.ID] = !done
		}
		if !newest && !anyTrue(pending) {
			continue
		}

		field, used := estimateMotion(history, func(t int64) *radar.Mosaic { return h.grids[t] },
			p.cfg.MaxGap, p.cfg.MotionPairs, h.pairs)
		if field == nil {
			continue // need at least two frames to see motion
		}
		reg := &region{cur: cur, field: field, used: used, frame: f.Time,
			trend: p.trendFor(func(t int64) *radar.Mosaic { return h.grids[t] }, f.Time, field)}
		for _, s := range h.stations {
			if !pending[s.ID] && !newest {
				continue
			}
			snap := p.snapshot(s.Lat, s.Lon, reg)
			snap.Station = s.ID
			if pending[s.ID] {
				if err := p.saveIssue(ctx, s.ID, snap, p.trendResult(s.Lat, s.Lon, reg)); err != nil {
					return err
				}
				issued++
			}
			if newest {
				p.mu.Lock()
				p.latest[s.ID] = snap
				p.mu.Unlock()
			}
		}
		if newest {
			// Station regions double as cached regions for on-demand requests.
			p.putRegion(regionKey{h.center.X, h.center.Y, f.Time}, reg)
		}
	}
	if issued > 0 {
		p.log.Info("forecasts issued", "time", time.Unix(f.Time, 0).Format(time.TimeOnly), "stations", issued)
	}
	return nil
}

func anyTrue(m map[string]bool) bool {
	for _, v := range m {
		if v {
			return true
		}
	}
	return false
}

// saveIssue stores a station forecast and, when available, the shadow
// forecast with intensity trend at the same leads.
func (p *Pipeline) saveIssue(ctx context.Context, station string, snap *Snapshot, trend *nowcast.Result) error {
	js, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	res := snap.Result
	persist := float64(res.At(0))
	leads := make([]store.Forecast, 0, len(p.cfg.Leads))
	for _, l := range p.cfg.Leads {
		pred := float64(res.At(l))
		f := store.Forecast{
			LeadMin: l, PredDBZ: pred, PersistDBZ: persist,
			PredRain: pred >= float64(p.cfg.Threshold), PersistRain: res.RainingNow,
		}
		if trend != nil {
			v := float64(trend.At(l))
			f.Trend = &store.TrendForecast{DBZ: v, Rain: v >= float64(p.cfg.Threshold)}
		}
		leads = append(leads, f)
	}
	return p.store.SaveIssue(ctx, station, snap.FrameTime.Unix(), res.ArrivalMin, res.RainingNow, js, leads)
}

// estimateMotion averages motion over the last `pairs` frame pairs, newest
// weighted most (weights pairs, pairs-1, …, 1), stopping at a gap longer
// than maxGap. Each pair's field is cached in cache (keyed by its later
// frame) when cache is non-nil. It returns the field and the number of
// frames used.
func estimateMotion(history []rainviewer.Frame, grid func(int64) *radar.Mosaic, maxGap time.Duration, pairs int, cache map[int64]*motion.Field) (*motion.Field, int) {
	opt := motion.DefaultOptions()
	var fields []*motion.Field
	var weights []float64
	used := 1
	for k := len(history) - 1; k >= 1 && len(fields) < pairs; k-- {
		a, b := history[k-1], history[k]
		ga, gb := grid(a.Time), grid(b.Time)
		gap := time.Duration(b.Time-a.Time) * time.Second
		if ga == nil || gb == nil || gap > maxGap {
			break
		}
		f, ok := cache[b.Time]
		if !ok {
			f = motion.Estimate(ga.Grid, gb.Grid, gap.Minutes(), opt)
			if cache != nil {
				cache[b.Time] = f
			}
		}
		fields = append(fields, f)
		weights = append(weights, float64(pairs-len(weights)))
		used++
	}
	if len(fields) == 0 {
		return nil, 0
	}
	return motion.WeightedAverage(fields, weights), used
}
