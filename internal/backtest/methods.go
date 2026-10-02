package backtest

import (
	"raincast/internal/model"
	"raincast/internal/motion"
	"raincast/internal/radar"
)

// window caches the frames a region's issues need, dropping those older
// than the longest history; motion comes from the model package.
type window struct {
	src     Source
	cov     *radar.Coverage
	cfg     Config
	grids   map[int64]*radar.Mosaic // nil value: missing
	rainy   map[int64]bool          // central tile rains (for events)
	skipped int
	motion  *model.Builder
	cache   *model.Cache
	fields  map[fieldKey]*motion.Field // combined fields for the current issue
}

type fieldKey struct {
	method string
	pairs  int
}

func newWindow(src Source, cov *radar.Coverage, cfg Config) *window {
	w := &window{src: src, cov: cov, cfg: cfg, grids: map[int64]*radar.Mosaic{}, rainy: map[int64]bool{},
		cache: model.NewCache(), fields: map[fieldKey]*motion.Field{}}
	w.motion = &model.Builder{
		Grid: func(t int64) *radar.Grid {
			if m := w.grid(t); m != nil {
				return m.Grid
			}
			return nil
		},
		// Frames are a fixed step apart; a missing one is a gap.
		Prev: func(t int64) (int64, bool) {
			p := t - cfg.StepSec
			return p, w.grid(p) != nil
		},
		Workers: cfg.Workers,
		Cache:   w.cache,
	}
	return w
}

func (w *window) grid(t int64) *radar.Mosaic {
	if m, ok := w.grids[t]; ok {
		return m
	}
	m := w.src.Load(t)
	w.grids[t] = m
	if m == nil {
		w.skipped++
		return nil
	}
	ts := m.W / 3
	w.rainy[t] = radar.RainFraction(m.Grid, w.cov, ts, ts, ts, ts, w.cfg.Threshold) >= w.cfg.EventRain
	return m
}

func (w *window) evict(before int64) {
	for t := range w.grids {
		if t < before {
			delete(w.grids, t)
		}
	}
	w.cache.Evict(before)
	clear(w.fields)
}

// complete reports whether frames exist every step from t+from to t+to steps.
func (w *window) complete(t int64, from, to int) bool {
	for k := from; k <= to; k++ {
		if w.grid(t+int64(k)*w.cfg.StepSec) == nil {
			return false
		}
	}
	return true
}

func (w *window) hasLeads(t int64) bool {
	for _, l := range w.cfg.Leads {
		if w.grid(t+int64(l)*60) == nil {
			return false
		}
	}
	return true
}

// field is the motion a method extrapolates along for issue time t.
func (w *window) field(method string, t int64, pairs int) *motion.Field {
	k := fieldKey{method, pairs}
	if f, ok := w.fields[k]; ok {
		return f
	}
	f, _ := w.motion.Field(method, t, pairs)
	w.fields[k] = f
	return f
}

// trend is the intensity growth along field over the last 20 minutes (10
// when the earlier frame is missing).
func (w *window) trend(t int64, field *motion.Field) *motion.Trend {
	return w.motion.Trend(t, field, w.cfg.Threshold-5)
}
