package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"raincast/internal/geo"
	"raincast/internal/motion"
	"raincast/internal/radar"
	"raincast/internal/rainviewer"
)

// ErrNotReady means the radar index has not been loaded yet.
var ErrNotReady = errors.New("pipeline: radar index not loaded yet")

// maxRegions bounds the on-demand cache (~2.5 MB per region).
const maxRegions = 16

// regionKey identifies a 3×3 tile block by its center tile and radar frame.
type regionKey struct {
	TX, TY int
	Frame  int64
}

// region is the newest mosaic around a center tile plus its motion field.
// Any point inside the center tile has at least one tile of margin.
type region struct {
	cur   *radar.Mosaic
	field *motion.Field
	trend *motion.Trend // intensity growth/decay; nil without an earlier frame
	used  int
	frame int64
}

// ForecastAt computes a forecast for any location using the newest frames.
// Results are not stored; only the configured home location is verified.
func (p *Pipeline) ForecastAt(ctx context.Context, lat, lon float64) (*Snapshot, error) {
	p.mu.RLock()
	host, frames := p.host, p.frames
	p.mu.RUnlock()
	if len(frames) < 2 {
		return nil, ErrNotReady
	}
	gx, gy := geo.LatLonToPixel(lat, lon, p.cfg.Zoom)
	tx, ty := geo.TileOf(gx, gy)
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
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		return p.buildRegion(ctx, key, host, frames)
	})
	if err != nil {
		return nil, err
	}
	return v.(*region), nil
}

// buildRegion loads the last MotionPairs+1 frames around the key's tile and
// estimates motion from them.
func (p *Pipeline) buildRegion(ctx context.Context, key regionKey, host string, frames []rainviewer.Frame) (*region, error) {
	history := frames[max(0, len(frames)-p.cfg.MotionPairs-1):]
	grids := make(map[int64]*radar.Mosaic, len(history))
	for _, f := range history {
		m, err := p.loadMosaic(ctx, host, f, key.TX-1, key.TY-1)
		if err != nil {
			return nil, err
		}
		grids[f.Time] = m
	}
	field, used := estimateMotion(history, func(t int64) *radar.Mosaic { return grids[t] }, p.cfg.MaxGap, p.cfg.MotionPairs, nil)
	if field == nil {
		return nil, fmt.Errorf("pipeline: frames too far apart to estimate motion")
	}
	r := &region{cur: grids[key.Frame], field: field, used: used, frame: key.Frame,
		trend: p.trendFor(func(t int64) *radar.Mosaic { return grids[t] }, key.Frame, field)}
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
