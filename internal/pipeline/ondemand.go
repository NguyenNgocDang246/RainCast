package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"math"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"raincast/internal/cache"
	"raincast/internal/geo"
	"raincast/internal/model"
	"raincast/internal/motion"
	"raincast/internal/radar"
	"raincast/internal/rainviewer"
)

// ErrNotReady means the radar index has not been loaded yet.
var ErrNotReady = errors.New("pipeline: radar index not loaded yet")

// ErrStaleTiles means uploaded tiles are not those of the newest frames
// (a new frame arrived meanwhile); the client should ask for a new plan.
var ErrStaleTiles = errors.New("pipeline: tiles are not from the newest radar frames")

// ErrBadTiles means uploaded tiles do not match the plan or are not radar
// tiles.
var ErrBadTiles = errors.New("pipeline: tiles do not match the plan")

// maxRegions bounds the on-demand cache (~1 MB per region).
const maxRegions = 16

// regionTimeout bounds loading a region nobody asked about before: up to
// (pairs+1)·4 tiles within this process's share of RainViewer's limit.
const regionTimeout = 90 * time.Second

// Shared cache lifetimes. Regions are only served while their frame is the
// newest (10 minutes); motion pairs are reused by the next pairs frames.
const (
	sharedRegionTTL = 30 * time.Minute
	sharedMotionTTL = time.Hour
)

// regionTiles is the side of a region in tiles. A point is always at least
// half a tile (128 px, ~150 km at the equator) inside its region: more than
// rain travels in an hour (the motion search caps it at ~96 px), so only
// the 2×2 tiles on the point's side of its tile are needed, not 3×3.
const regionTiles = 2

// regionCorner is the top-left tile of the 2×2 block that keeps global
// pixel (gx, gy) at least half a tile from every edge.
func regionCorner(gx, gy float64) (int, int) {
	corner := func(v float64) int {
		t := int(math.Floor(v / geo.TileSize))
		if v-float64(t*geo.TileSize) < geo.TileSize/2 {
			return t - 1
		}
		return t
	}
	return corner(gx), corner(gy)
}

// maxMotionCaches bounds the per-tile-block motion caches (each holds a few
// small block-grid fields per method).
const maxMotionCaches = 64

// tileKey identifies a 2×2 tile block across frames.
type tileKey struct{ TX, TY int }

// motionCache keeps a tile block's per-pair motion between frames: when a
// new frame arrives only its pair is estimated, not every pair again.
type motionCache struct {
	cache *model.Cache
	used  int64 // newest frame it served
}

// region is the newest mosaic around a point plus the model's motion for
// it, shared by every point on the same side of the same tile.
type region struct {
	cur   *radar.Mosaic
	prep  *model.Prepared
	frame int64
	tiles map[radar.TileKey][]byte // cur's tiles, to share the region
}

// TileID names one radar tile of a frame.
type TileID struct {
	Time int64 `json:"time"`
	X    int   `json:"x"`
	Y    int   `json:"y"`
}

// TileRef is a tile a forecast needs and where to download it.
type TileRef struct {
	TileID
	URL string `json:"url"`
}

// TilePlan lists the tiles a forecast for a point reads: its 2×2 tile block
// in each of the newest frames, oldest frame first.
type TilePlan struct {
	Frame int64     `json:"frame"` // the newest frame, forecasts start from it
	Tiles []TileRef `json:"tiles"`
}

// ContentHash names a set of tiles by their content, in plan order: the hex
// SHA-256 of one "time:x:y:<hex SHA-256 of the PNG>\n" line per tile. Clients
// compute the same to ask for a cached forecast (GET /api/forecast?h=).
func ContentHash(order []TileID, tiles map[TileID][]byte) string {
	h := sha256.New()
	for _, id := range order {
		sum := sha256.Sum256(tiles[id])
		fmt.Fprintf(h, "%d:%d:%d:%x\n", id.Time, id.X, id.Y, sum)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// index returns the current frame index.
func (p *Pipeline) index() (string, []rainviewer.Frame) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.host, p.frames
}

// history is the newest frames the model's longest motion reads.
func (p *Pipeline) history(frames []rainviewer.Frame) []rainviewer.Frame {
	pairs := 1
	for _, m := range p.cfg.Model.Members {
		pairs = max(pairs, m.Pairs)
	}
	return frames[max(0, len(frames)-pairs-1):]
}

// blockTiles are the tiles of the 2×2 block whose top-left is (tx, ty),
// skipping those outside the world (near the poles or the antimeridian).
func (p *Pipeline) blockTiles(tx, ty int) []rainviewer.Tile {
	n := 1 << p.cfg.Zoom
	var tiles []rainviewer.Tile
	for dy := range regionTiles {
		for dx := range regionTiles {
			x, y := tx+dx, ty+dy
			if x >= 0 && y >= 0 && x < n && y < n {
				tiles = append(tiles, rainviewer.Tile{Z: p.cfg.Zoom, X: x, Y: y})
			}
		}
	}
	return tiles
}

// ForecastAt computes a forecast for any location using the newest frames,
// downloading the radar tiles itself. Results are not stored.
func (p *Pipeline) ForecastAt(ctx context.Context, lat, lon float64) (*Snapshot, error) {
	host, frames := p.index()
	if len(frames) < 2 {
		return nil, ErrNotReady
	}
	tx, ty := regionCorner(geo.LatLonToPixel(lat, lon, p.cfg.Zoom))
	frame := frames[len(frames)-1].Time
	key := fmt.Sprintf("f/%d/%d/%d", tx, ty, frame)
	r, err := p.regionFor(ctx, key, func(ctx context.Context) (*region, error) {
		hist := p.history(frames)
		raw := make(map[TileID][]byte)
		var mu sync.Mutex
		g, gctx := errgroup.WithContext(ctx)
		for _, f := range hist {
			g.Go(func() error {
				got, err := p.client.FetchTiles(gctx, host, f.Path, p.blockTiles(tx, ty))
				if err != nil {
					return err
				}
				mu.Lock()
				for t, data := range got {
					raw[TileID{f.Time, t.X, t.Y}] = data
				}
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
		return p.buildRegion(ctx, tx, ty, hist, raw)
	})
	if err != nil {
		return nil, err
	}
	return p.snapshot(lat, lon, r), nil
}

// Plan lists the tiles a forecast for (lat, lon) needs, for clients that
// download them (ForecastFromTiles).
func (p *Pipeline) Plan(lat, lon float64) (TilePlan, error) {
	host, frames := p.index()
	if len(frames) < 2 {
		return TilePlan{}, ErrNotReady
	}
	tx, ty := regionCorner(geo.LatLonToPixel(lat, lon, p.cfg.Zoom))
	plan := TilePlan{Frame: frames[len(frames)-1].Time}
	for _, f := range p.history(frames) {
		for _, t := range p.blockTiles(tx, ty) {
			plan.Tiles = append(plan.Tiles, TileRef{TileID{f.Time, t.X, t.Y}, rainviewer.TileURL(host, f.Path, t)})
		}
	}
	return plan, nil
}

// Cached returns the forecast for (lat, lon) from tiles whose ContentHash
// is hash, when a region built from exactly those tiles is cached and is
// still the newest frame's.
func (p *Pipeline) Cached(ctx context.Context, lat, lon float64, hash string) (*Snapshot, bool) {
	_, frames := p.index()
	if len(frames) < 2 || hash == "" {
		return nil, false
	}
	tx, ty := regionCorner(geo.LatLonToPixel(lat, lon, p.cfg.Zoom))
	r, ok := p.lookupRegion(ctx, "h/"+hash)
	if !ok || r.frame != frames[len(frames)-1].Time || r.cur.TileX != tx || r.cur.TileY != ty {
		return nil, false
	}
	return p.snapshot(lat, lon, r), true
}

// ForecastFromTiles forecasts (lat, lon) from tiles the client downloaded
// as Plan listed. The region is cached under the tiles' ContentHash, so
// tiles that differ from RainViewer's (tampered with) never reach anyone
// else's forecast. It returns ErrStaleTiles when the tiles are not the
// newest frames' and ErrBadTiles when they are not the plan's.
func (p *Pipeline) ForecastFromTiles(ctx context.Context, lat, lon float64, tiles map[TileID][]byte) (*Snapshot, error) {
	plan, err := p.Plan(lat, lon)
	if err != nil {
		return nil, err
	}
	order := make([]TileID, len(plan.Tiles))
	for i, t := range plan.Tiles {
		order[i] = t.TileID
	}
	if err := checkTiles(order, tiles); err != nil {
		return nil, err
	}
	_, frames := p.index()
	hist := p.history(frames)
	if hist[len(hist)-1].Time != plan.Frame {
		return nil, ErrStaleTiles // the index moved on since Plan
	}
	tx, ty := regionCorner(geo.LatLonToPixel(lat, lon, p.cfg.Zoom))
	r, err := p.regionFor(ctx, "h/"+ContentHash(order, tiles), func(ctx context.Context) (*region, error) {
		r, err := p.buildRegion(ctx, tx, ty, hist, tiles)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadTiles, err)
		}
		return r, nil
	})
	if err != nil {
		return nil, err
	}
	return p.snapshot(lat, lon, r), nil
}

// checkTiles reports whether tiles are exactly order's, each a 256 px PNG.
func checkTiles(order []TileID, tiles map[TileID][]byte) error {
	want := make(map[int64]bool)
	for _, id := range order {
		want[id.Time] = true
	}
	for id := range tiles {
		if !want[id.Time] {
			return ErrStaleTiles
		}
	}
	if len(tiles) != len(order) {
		return fmt.Errorf("%w: got %d tiles, want %d", ErrBadTiles, len(tiles), len(order))
	}
	for _, id := range order {
		data, ok := tiles[id]
		if !ok {
			return fmt.Errorf("%w: missing tile %d/%d of frame %d", ErrBadTiles, id.X, id.Y, id.Time)
		}
		// Checked before decoding, so a huge image is never decoded.
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != geo.TileSize || cfg.Height != geo.TileSize {
			return fmt.Errorf("%w: tile %d/%d of frame %d is not a 256 px PNG", ErrBadTiles, id.X, id.Y, id.Time)
		}
	}
	return nil
}

// regionFor returns the region cached under key, from memory or the shared
// cache, or builds it. Concurrent requests for the same key share one
// build, which must not die with whichever request happened to start it.
func (p *Pipeline) regionFor(ctx context.Context, key string, build func(context.Context) (*region, error)) (*region, error) {
	if r, ok := p.lookupRegion(ctx, key); ok {
		return r, nil
	}
	v, err, _ := p.flight.Do("region "+key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), regionTimeout)
		defer cancel()
		start := time.Now()
		r, err := build(ctx)
		if err != nil {
			return nil, err
		}
		p.putRegion(key, r)
		p.shareRegion(ctx, key, r)
		p.log.Info("region", "cache", "miss", "backend", cache.Backend(p.cfg.Shared), "key", key, "dur", time.Since(start))
		return r, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*region), nil
}

// lookupRegion finds key in memory, then in the shared cache.
func (p *Pipeline) lookupRegion(ctx context.Context, key string) (*region, bool) {
	p.mu.RLock()
	r, ok := p.regions[key]
	p.mu.RUnlock()
	if ok {
		p.log.Debug("region", "cache", "hit", "backend", "memory", "key", key)
		return r, true
	}
	if p.cfg.Shared == nil {
		return nil, false
	}
	data, ok := p.cfg.Shared.Get(ctx, "rc:region:"+key)
	if !ok {
		return nil, false
	}
	r, err := decodeRegion(data)
	if err != nil {
		p.log.Warn("shared region", "key", key, "err", err)
		return nil, false
	}
	p.putRegion(key, r)
	p.log.Info("region", "cache", "hit", "backend", "redis", "key", key)
	return r, true
}

// regionWire is a region as stored in the shared cache: the newest frame's
// tiles (a few KB, decoded again on load) instead of its 1 MB grid.
type regionWire struct {
	Frame        int64
	Zoom, TX, TY int
	Tiles        map[radar.TileKey][]byte
	Prepared     []byte
}

func (p *Pipeline) shareRegion(ctx context.Context, key string, r *region) {
	if p.cfg.Shared == nil {
		return
	}
	prep, err := r.prep.MarshalBinary()
	if err != nil {
		p.log.Warn("share region", "err", err)
		return
	}
	var buf bytes.Buffer
	w := regionWire{Frame: r.frame, Zoom: r.cur.Zoom, TX: r.cur.TileX, TY: r.cur.TileY, Tiles: r.tiles, Prepared: prep}
	if err := gob.NewEncoder(&buf).Encode(w); err != nil {
		p.log.Warn("share region", "err", err)
		return
	}
	p.cfg.Shared.Set(ctx, "rc:region:"+key, buf.Bytes(), sharedRegionTTL)
}

func decodeRegion(data []byte) (*region, error) {
	var w regionWire
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&w); err != nil {
		return nil, err
	}
	cur, err := radar.BuildMosaic(w.Tiles, w.Zoom, w.TX, w.TY, regionTiles, radar.DefaultPalette)
	if err != nil {
		return nil, err
	}
	prep := &model.Prepared{}
	if err := prep.UnmarshalBinary(w.Prepared); err != nil {
		return nil, err
	}
	return &region{cur: cur, prep: prep, frame: w.Frame, tiles: w.Tiles}, nil
}

// buildRegion decodes the 2×2 block at (tx, ty) of every frame in hist
// (oldest first) from raw and estimates motion over them. Motion pairs are
// cached by the hash of both frames' tiles, so a pair is estimated once
// however many requests, or processes through the shared cache, need it.
func (p *Pipeline) buildRegion(ctx context.Context, tx, ty int, hist []rainviewer.Frame, raw map[TileID][]byte) (*region, error) {
	if len(hist) < 2 {
		return nil, ErrNotReady
	}
	grids := make(map[int64]*radar.Mosaic, len(hist))
	frameHash := make(map[int64]string, len(hist))
	var mu sync.Mutex
	g, _ := errgroup.WithContext(ctx)
	for _, f := range hist {
		g.Go(func() error {
			tiles := make(map[radar.TileKey][]byte)
			var order []TileID
			for _, t := range p.blockTiles(tx, ty) {
				id := TileID{f.Time, t.X, t.Y}
				tiles[radar.TileKey{X: t.X, Y: t.Y}] = raw[id]
				order = append(order, id)
			}
			m, err := radar.BuildMosaic(tiles, p.cfg.Zoom, tx, ty, regionTiles, radar.DefaultPalette)
			if err != nil {
				return fmt.Errorf("frame %d: %w", f.Time, err)
			}
			h := ContentHash(order, raw)
			mu.Lock()
			grids[f.Time], frameHash[f.Time] = m, h
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	frame := hist[len(hist)-1].Time
	start := time.Now()
	// Pairs end at hist[1:]; the one ending at hist[0] has no earlier
	// frame here.
	oldest := hist[1].Time
	mc := p.motionFor(tx, ty, frame, oldest)
	mc.Evict(oldest)
	reused := mc.Len()
	b := p.builder(hist, func(t int64) *radar.Mosaic { return grids[t] }, mc)
	b.PairID = func(t int64) string {
		prev, ok := b.Prev(t)
		if !ok {
			return ""
		}
		return frameHash[prev] + frameHash[t]
	}
	prep := p.cfg.Model.Prepare(b, frame, p.cfg.Threshold-5)
	if prep == nil {
		return nil, fmt.Errorf("pipeline: frames too far apart to estimate motion")
	}
	p.log.Debug("region built", "tx", tx, "ty", ty, "pairs_reused", reused,
		"pairs_estimated", mc.Len()-reused, "dur", time.Since(start))
	cur := make(map[radar.TileKey][]byte)
	for _, t := range p.blockTiles(tx, ty) {
		cur[radar.TileKey{X: t.X, Y: t.Y}] = raw[TileID{frame, t.X, t.Y}]
	}
	return &region{cur: grids[frame], prep: prep, frame: frame, tiles: cur}, nil
}

// motionFor returns the motion cache of the tile block at (tx, ty), marking
// it used by frame. Caches unused since before oldest (the earliest frame a
// forecast now reads) hold nothing useful and are dropped, and the oldest go
// first beyond maxMotionCaches.
func (p *Pipeline) motionFor(tx, ty int, frame, oldest int64) *model.Cache {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := tileKey{tx, ty}
	mc, ok := p.motion[k]
	if !ok {
		mc = &motionCache{cache: model.NewCache()}
		if p.cfg.Shared != nil {
			mc.cache.Shared = sharedFields{p.cfg.Shared}
		}
		p.motion[k] = mc
	}
	mc.used = max(mc.used, frame)
	for k, c := range p.motion {
		if c.used < oldest {
			delete(p.motion, k)
		}
	}
	for len(p.motion) > maxMotionCaches {
		var stale tileKey
		first := true
		for k, c := range p.motion {
			if first || c.used < p.motion[stale].used {
				stale, first = k, false
			}
		}
		delete(p.motion, stale)
	}
	return mc.cache
}

// sharedFields keeps motion fields in the shared cache.
type sharedFields struct{ c cache.Shared }

func (s sharedFields) Get(method, id string) (*motion.Field, bool) {
	data, ok := s.c.Get(context.Background(), "rc:motion:"+method+":"+id)
	if !ok {
		return nil, false
	}
	var f motion.Field
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&f); err != nil {
		return nil, false
	}
	return &f, true
}

func (s sharedFields) Put(method, id string, f *motion.Field) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(f); err != nil {
		return
	}
	s.c.Set(context.Background(), "rc:motion:"+method+":"+id, buf.Bytes(), sharedMotionTTL)
}

// putRegion caches r, dropping regions from older frames and, when full,
// one more to make room.
func (p *Pipeline) putRegion(key string, r *region) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, old := range p.regions {
		if old.frame < r.frame {
			delete(p.regions, k)
		}
	}
	if _, ok := p.regions[key]; !ok && len(p.regions) >= maxRegions {
		for k := range p.regions {
			delete(p.regions, k)
			break
		}
	}
	p.regions[key] = r
}
