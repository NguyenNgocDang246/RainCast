// Package model holds RainCast's forecast models: the ways of estimating
// how rain moves (block matching and optical flow) and the
// ensembles that combine them. The app forecasts with Default(); the
// backtest scores every method so the default can be chosen again as data
// accumulates.
package model

import (
	"sync"

	"raincast/internal/cell"
	"raincast/internal/flow"
	"raincast/internal/motion"
	"raincast/internal/radar"
)

// Methods of estimating motion. Each yields a motion field that the
// semi-Lagrangian nowcast extrapolates along.
const (
	TREC = "trec" // block matching by cross-correlation
	HS   = "hs"   // Horn–Schunck optical flow
	LK   = "lk"   // pyramidal Lucas–Kanade optical flow
)

// Methods lists every motion method.
var Methods = []string{TREC, HS, LK}

// Cache keeps per-pair motion fields between calls, so a frame pair is
// estimated once per method however many forecasts use it. It is safe for
// concurrent use.
type Cache struct {
	mu    sync.Mutex
	pairs map[pairKey]*motion.Field
	// Shared, when set, is asked for pairs this cache lacks and given every
	// pair it estimates, for pairs whose Builder names their content
	// (Builder.PairID).
	Shared FieldStore
}

// FieldStore keeps motion fields by method and pair content beyond one
// Cache, e.g. in a cache shared between processes.
type FieldStore interface {
	Get(method, id string) (*motion.Field, bool)
	Put(method, id string, f *motion.Field)
}

// pairKey is a frame pair by its newer frame's time and, when the Builder
// names it, its content: pairs of the same times built from other tiles
// are other pairs.
type pairKey struct {
	method string
	t      int64
	id     string
}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{pairs: map[pairKey]*motion.Field{}} }

// Evict drops pairs ending before t.
func (c *Cache) Evict(before int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.pairs {
		if k.t < before {
			delete(c.pairs, k)
		}
	}
}

// Len is the number of cached pairs.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pairs)
}

func (c *Cache) get(k pairKey) (*motion.Field, bool) {
	c.mu.Lock()
	f, ok := c.pairs[k]
	c.mu.Unlock()
	if ok || c.Shared == nil || k.id == "" {
		return f, ok
	}
	if f, ok = c.Shared.Get(k.method, k.id); ok {
		c.mu.Lock()
		c.pairs[k] = f
		c.mu.Unlock()
	}
	return f, ok
}

func (c *Cache) put(k pairKey, f *motion.Field) {
	c.mu.Lock()
	c.pairs[k] = f
	c.mu.Unlock()
	if c.Shared != nil && k.id != "" {
		c.Shared.Put(k.method, k.id, f)
	}
}

// Builder estimates motion fields over a run of frames.
type Builder struct {
	// Grid returns the frame at t, or nil when it is missing.
	Grid func(t int64) *radar.Grid
	// Prev returns the frame before t to pair it with, or false when there
	// is none close enough (a gap).
	Prev func(t int64) (int64, bool)
	// Workers is the goroutines per estimate; 0 means GOMAXPROCS.
	Workers int
	Cache   *Cache
	// PairID, when set, names the content of the pair ending at t (say, a
	// hash of both frames' tiles), so cached fields are only reused for
	// the same radar data.
	PairID func(t int64) string
}

// pair is method's motion for the frame pair ending at t, or nil.
func (b *Builder) pair(method string, t int64) *motion.Field {
	key := pairKey{method: method, t: t}
	if b.PairID != nil {
		key.id = b.PairID(t)
	}
	if f, ok := b.Cache.get(key); ok {
		return f
	}
	p, ok := b.Prev(t)
	if !ok {
		return nil
	}
	prev, cur := b.Grid(p), b.Grid(t)
	if prev == nil || cur == nil {
		return nil
	}
	minutes := float64(t-p) / 60
	var f *motion.Field
	switch method {
	case HS:
		opt := flow.DefaultHS()
		opt.Workers = b.Workers
		f = flow.HornSchunck(prev, cur, minutes, opt)
	case LK:
		f = flow.LucasKanade(prev, cur, minutes, flow.DefaultLK())
	default: // TREC and everything built on it
		opt := motion.DefaultOptions()
		if b.Workers > 0 {
			opt.Workers = b.Workers
		}
		f = motion.Estimate(prev, cur, minutes, opt)
	}
	b.Cache.put(key, f)
	return f
}

// chain returns up to n frame times ending at t, newest first, stopping at
// a gap.
func (b *Builder) chain(t int64, n int) []int64 {
	out := []int64{t}
	for len(out) <= n {
		p, ok := b.Prev(out[len(out)-1])
		if !ok || b.Grid(p) == nil {
			break
		}
		out = append(out, p)
	}
	return out
}

// Field is the motion method extrapolates along for frame t, averaged over
// up to pairs frame pairs (newest weighted most), and how many frames it
// used; nil when there is not even one pair.
func (b *Builder) Field(method string, t int64, pairs int) (*motion.Field, int) {
	times := b.chain(t, pairs)
	if len(times) < 2 {
		return nil, 0
	}
	n := len(times) - 1
	switch method {
	case TREC, HS, LK:
		fields := make([]*motion.Field, n)
		weights := make([]float64, n)
		for k := range n {
			fields[k] = b.pair(method, times[k])
			weights[k] = float64(pairs - k)
		}
		return motion.WeightedAverage(fields, weights), n + 1
	}
	panic("model: unknown method " + method)
}

// Storms follows the convective cells of frame t back through up to pairs
// earlier frames along field.
func (b *Builder) Storms(t int64, field *motion.Field, pairs int) []cell.Storm {
	if field == nil {
		return nil
	}
	times := b.chain(t, pairs)
	if len(times) < 2 {
		return nil
	}
	frames := make([]*radar.Grid, len(times))
	for k, ft := range times {
		frames[len(times)-1-k] = b.Grid(ft) // oldest first
	}
	minutes := float64(times[0]-times[1]) / 60
	return cell.Lifecycle(frames, field, minutes, cell.DefaultOptions(cell.MatchHungarian))
}

// Trend is the intensity growth along field at frame t, measured over two
// frames back (20 minutes) or one when that is all there is; nil without
// an earlier frame.
func (b *Builder) Trend(t int64, field *motion.Field, rainDBZ float32) *motion.Trend {
	if field == nil {
		return nil
	}
	times := b.chain(t, 2)
	if len(times) < 2 {
		return nil
	}
	p := times[len(times)-1]
	return motion.EstimateTrend(b.Grid(p), b.Grid(t), field, float64(t-p)/60, rainDBZ)
}
