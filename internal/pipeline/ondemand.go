package pipeline

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"raincast/internal/geo"
	"raincast/internal/model"
	"raincast/internal/radar"
	"raincast/internal/rainviewer"
)

// ErrNotReady means the radar index has not been loaded yet.
var ErrNotReady = errors.New("pipeline: radar index not loaded yet")

// maxRegions bounds the on-demand cache (~1 MB per region).
const maxRegions = 16

// regionTimeout bounds loading a region nobody asked about before: up to
// (pairs+1)·4 tiles within this process's share of RainViewer's limit.
const regionTimeout = 90 * time.Second

// regionTiles is the side of a region in tiles. A point is always at least
// half a tile (128 px, ~150 km at the equator) inside its region: more than
// rain travels in an hour (the motion search caps it at ~96 px), so only
// the 2×2 tiles on the point's side of its tile are needed, not 3×3.
const regionTiles = 2

// regionKey identifies a 2×2 tile block by its top-left tile and radar frame.
type regionKey struct {
	TX, TY int
	Frame  int64
}

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

// region is the newest mosaic around a point plus the model's motion for
// it, shared by every point on the same side of the same tile.
type region struct {
	cur   *radar.Mosaic
	prep  *model.Prepared
	frame int64
}

// ForecastAt computes a forecast for any location using the newest frames.
// Results are not stored.
func (p *Pipeline) ForecastAt(ctx context.Context, lat, lon float64) (*Snapshot, error) {
	p.mu.RLock()
	host, frames := p.host, p.frames
	p.mu.RUnlock()
	if len(frames) < 2 {
		return nil, ErrNotReady
	}
	tx, ty := regionCorner(geo.LatLonToPixel(lat, lon, p.cfg.Zoom))
	key := regionKey{tx, ty, frames[len(frames)-1].Time}

	r, err := p.regionFor(ctx, key, host, frames)
	if err != nil {
		return nil, err
	}
	return p.snapshot(lat, lon, r), nil
}

func (p *Pipeline) regionFor(ctx context.Context, key regionKey, host string, frames []rainviewer.Frame) (*region, error) {
	p.mu.RLock()
	r, ok := p.regions[key]
	p.mu.RUnlock()
	if ok {
		return r, nil
	}
	// Concurrent requests for the same region share one computation, which
	// must not die with whichever request happened to start it.
	v, err, _ := p.flight.Do(fmt.Sprint(key), func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), regionTimeout)
		defer cancel()
		return p.buildRegion(ctx, key, host, frames)
	})
	if err != nil {
		return nil, err
	}
	return v.(*region), nil
}

// buildRegion loads, all at once, the frames the model's longest motion
// needs over the key's tiles and estimates motion from them.
func (p *Pipeline) buildRegion(ctx context.Context, key regionKey, host string, frames []rainviewer.Frame) (*region, error) {
	pairs := 1
	for _, m := range p.cfg.Model.Members {
		pairs = max(pairs, m.Pairs)
	}
	history := frames[max(0, len(frames)-pairs-1):]
	grids := make(map[int64]*radar.Mosaic, len(history))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for _, f := range history {
		g.Go(func() error {
			m, err := p.loadMosaic(gctx, host, f, key.TX, key.TY, regionTiles)
			if err != nil {
				return err
			}
			mu.Lock()
			grids[f.Time] = m
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	b := p.builder(history, func(t int64) *radar.Mosaic { return grids[t] }, model.NewCache())
	prep := p.cfg.Model.Prepare(b, key.Frame, p.cfg.Threshold-5)
	if prep == nil {
		return nil, fmt.Errorf("pipeline: frames too far apart to estimate motion")
	}
	r := &region{cur: grids[key.Frame], prep: prep, frame: key.Frame}
	p.putRegion(key, r)
	return r, nil
}

// putRegion caches r, dropping regions from older frames and capping size.
func (p *Pipeline) putRegion(key regionKey, r *region) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.regions {
		if k.Frame != key.Frame || len(p.regions) >= maxRegions {
			delete(p.regions, k)
		}
	}
	p.regions[key] = r
}
