package pipeline

import (
	"slices"

	"raincast/internal/cell"
	"raincast/internal/geo"
	"raincast/internal/ml"
	"raincast/internal/model"
	"raincast/internal/nowcast"
	"raincast/internal/radar"
)

// mlEnabled reports whether regions gather what Config.ML reads: a model
// is configured and the forecasts are the ones it was trained on, the
// trended mean of every ml.Members method.
func (p *Pipeline) mlEnabled() bool {
	if p.cfg.ML == nil || !p.cfg.Model.Trend {
		return false
	}
	for _, m := range ml.Members {
		if !slices.ContainsFunc(p.cfg.Model.Members, func(mem model.Member) bool { return mem.Method == m }) {
			return false
		}
	}
	return true
}

// memberPairs is the frame pairs the model's member using method averages.
func (p *Pipeline) memberPairs(method string) int {
	for _, m := range p.cfg.Model.Members {
		if m.Method == method {
			return m.Pairs
		}
	}
	return 1
}

// addScene gathers what the ML layer reads of r, built from raw by b: the
// storms' lives along Lucas–Kanade's motion and the frame before, whose
// tiles r keeps to share the region. A no-op when the model is off or
// failed to load.
func (p *Pipeline) addScene(r *region, b *model.Builder, raw map[TileID][]byte) {
	if !p.mlEnabled() || p.cfg.ML() == nil {
		return
	}
	lk, _, _ := r.prep.Member(model.LK)
	storms := b.Storms(r.frame, lk, p.memberPairs(model.LK))
	var prev *radar.Grid
	var minutes float64
	if pt, ok := b.Prev(r.frame); ok {
		if prev = b.Grid(pt); prev != nil {
			minutes = float64(r.frame-pt) / 60
			r.prevTiles = make(map[radar.TileKey][]byte)
			for _, t := range p.blockTiles(r.cur.TileX, r.cur.TileY) {
				r.prevTiles[radar.TileKey{X: t.X, Y: t.Y}] = raw[TileID{pt, t.X, t.Y}]
			}
		}
	}
	r.prevMinutes = minutes
	r.scene = newScene(r.cur, r.prep, r.frame, storms, prev, minutes)
}

// newScene is the ml.Scene of a region; Lat, Lon and KmPerPx are the
// target's, set per forecast.
func newScene(cur *radar.Mosaic, prep *model.Prepared, frame int64, storms []cell.Storm, prev *radar.Grid, minutes float64) *ml.Scene {
	s := &ml.Scene{
		Grid:    cur.Grid,
		OriginX: float64(cur.TileX * geo.TileSize), OriginY: float64(cur.TileY * geo.TileSize),
		Storms: storms, Prev: prev, PrevMinutes: minutes, Time: frame,
	}
	for _, m := range ml.Members {
		f, tr, _ := prep.Member(m)
		s.Fields = append(s.Fields, f)
		s.Trends = append(s.Trends, tr)
	}
	s.Prime()
	return s
}

// applyML post-processes res, the trended forecast at pixel (x, y) of r for
// (lat, lon), with Config.ML, and reports whether it did.
func (p *Pipeline) applyML(r *region, lat, lon, x, y float64, opt nowcast.Options, res *nowcast.Result) bool {
	if r.scene == nil || !p.mlEnabled() {
		return false
	}
	b := p.cfg.ML()
	if b == nil {
		return false
	}
	s := *r.scene
	s.Lat, s.Lon, s.KmPerPx = lat, lon, opt.KmPerPx
	mopt := opt
	mopt.ProbRadius = ml.LeadProbRadius
	members := make([]nowcast.Result, len(ml.Members))
	for k, m := range ml.Members {
		f, tr, ok := r.prep.Member(m)
		if !ok {
			continue
		}
		o := mopt
		o.Trend = tr
		members[k] = nowcast.Forecast(r.cur.Grid, f, x, y, o)
	}
	if !b.Adjust(&s, x, y, members, res.Series) {
		return false
	}
	sum := nowcast.Summarize(res.Series, opt)
	res.RainingNow, res.ArrivalMin = sum.RainingNow, sum.ArrivalMin
	res.HeavyNow, res.HeavyArrivalMin = sum.HeavyNow, sum.HeavyArrivalMin
	res.AccumMM = sum.AccumMM
	return true
}
