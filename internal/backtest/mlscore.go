package backtest

import (
	"math"
	"slices"

	"raincast/internal/geo"
	"raincast/internal/himawari"
	"raincast/internal/ml"
	"raincast/internal/nwp"
	"raincast/internal/radar"
)

// MethodML post-processes the trended members with a learned model
// (package ml); Variant.MLSet names its feature set.
const MethodML = "ml"

// MLSetup gives the backtest what the ml package needs. Nil fields leave
// their features unknown (NaN).
type MLSetup struct {
	Sat *himawari.Client       // reads cached tiles only
	NWP map[string]*nwp.Series // by Region.Name
	// Dump, when set, receives every scored row for training; DumpEvery
	// keeps one sample point in DumpEvery across and down.
	Dump      *ml.Writer
	DumpEvery int
	// Bundles are, per feature set, the models for each fold: Bundles[s][k]
	// was trained without fold k and scores the regions in it.
	Bundles map[string][2]*ml.Bundle
}

// mlVariantNames label the ML variants.
var mlVariantNames = map[string]string{
	ml.SetRadar: "ML", ml.SetAll: "ML + NWP + vệ tinh", ml.SetNoNWP: "ML + vệ tinh", ml.SetNoSat: "ML + NWP",
	ml.SetEns: "Ensemble hiệu chỉnh",
}

// AddMLVariants appends a variant per feature set in sets, after the
// members they post-process.
func AddMLVariants(cfg *Config, sets []string) {
	var members []string
	for _, m := range ml.Members {
		members = append(members, cfg.memberName(m))
	}
	for _, s := range sets {
		cfg.Variants = append(cfg.Variants, Variant{Name: mlVariantNames[s], Method: MethodML, MLSet: s,
			Members: members, Trend: true})
	}
	// What the ML layer adds over the app's ensemble, and each other set
	// against it: the same ensemble calibrated alike, and what NWP or the
	// satellite add (read "sat" and "tropical_sat" for the satellite).
	main := ml.SetRadar
	if !slices.Contains(sets, main) {
		main = ml.SetAll
	}
	if !slices.Contains(sets, main) {
		return
	}
	for _, v := range cfg.Variants {
		if v.Method == MethodMean && v.Trend {
			cfg.Compare = append(cfg.Compare, [2]string{v.Name, mlVariantNames[main]})
			break
		}
	}
	for _, s := range sets {
		if s != main {
			cfg.Compare = append(cfg.Compare, [2]string{mlVariantNames[s], mlVariantNames[main]})
		}
	}
}

// memberName is the name of method's trended variant at motionPairs, or
// "" when there is none.
func (cfg *Config) memberName(method string) string {
	for _, v := range cfg.Variants {
		if v.Method == method && v.Trend && v.Pairs == motionPairs {
			return v.Name
		}
	}
	return ""
}

// mlNeeded reports whether issues must build ML rows.
func (cfg *Config) mlNeeded() bool {
	if cfg.ML == nil {
		return false
	}
	if cfg.ML.Dump != nil {
		return true
	}
	for _, v := range cfg.Variants {
		if v.Method == MethodML {
			return true
		}
	}
	return false
}

// mlIssue is one issue time's ML rows: rows[lead][point] in ml.Names order.
type mlIssue struct {
	rows [][][]float32
}

// buildML computes the rows of every point and lead at issue t, from the
// members' forecasts already in fc (members are fc indices, -1 missing).
func buildML(w *window, reg Region, cfg Config, t int64, cur *radar.Mosaic, points [][2]float64,
	fc []forecast, members []int) *mlIssue {
	s := &ml.Scene{
		Grid:    cur.Grid,
		OriginX: float64(cur.TileX * geo.TileSize), OriginY: float64(cur.TileY * geo.TileSize),
		KmPerPx: geo.MetersPerPixel(reg.Lat, cur.Zoom) / 1000,
		Time:    t, Lat: reg.Lat, Lon: reg.Lon,
	}
	if s.KmPerPx <= 0 || cur.Zoom == 0 {
		s.KmPerPx = geo.MetersPerPixel(reg.Lat, 7) / 1000
	}
	for _, m := range ml.Members {
		f := w.field(m, t, motionPairs)
		s.Fields = append(s.Fields, f)
		if f != nil {
			s.Trends = append(s.Trends, w.trend(t, m, motionPairs))
		} else {
			s.Trends = append(s.Trends, nil)
		}
	}
	if s.Fields[0] != nil {
		s.Storms = w.motion.Storms(t, s.Fields[0], motionPairs)
	}
	if prev := w.grid(t - cfg.StepSec); prev != nil {
		s.Prev, s.PrevMinutes = prev.Grid, float64(cfg.StepSec)/60
	}
	if cfg.ML.NWP != nil {
		s.NWP = cfg.ML.NWP[reg.Name]
	}
	if cfg.ML.Sat != nil && himawari.Covers(reg.Lat, reg.Lon) {
		tiles := himawari.RegionTiles(reg.TileX, reg.TileY, himawari.DefaultMargin)
		s.Sat = cfg.ML.Sat.LoadFrameFor(t, tiles)
		if s.Sat != nil {
			s.SatPast = cfg.ML.Sat.LoadFrame(s.Sat.Scan-1800, tiles)
		}
	}

	mi := &mlIssue{rows: make([][][]float32, len(cfg.Leads))}
	for li := range cfg.Leads {
		mi.rows[li] = make([][]float32, len(points))
	}
	vals := make([]float32, len(members))
	for pi, p := range points {
		pt := s.Prepare(p[0], p[1], cfg.Leads)
		for li, l := range cfg.Leads {
			var prob float32
			n := 0
			for k, m := range members {
				vals[k] = float32(math.NaN())
				if m >= 0 {
					vals[k] = fc[m].dbz[li][pi]
					prob += fc[m].prob[li][pi]
					n++
				}
			}
			if n > 0 {
				prob /= float32(n)
			}
			row := make([]float32, len(ml.Names))
			s.Row(pt, li, l, vals, prob, row)
			mi.rows[li][pi] = row
		}
	}
	return mi
}

// forecast scores variant v with the model that never saw the region's
// fold; nil when the set has no model.
func (mi *mlIssue) forecast(cfg Config, v Variant, fold int) *forecast {
	pair, ok := cfg.ML.Bundles[v.MLSet]
	if !ok {
		return nil
	}
	b := pair[fold]
	if b == nil {
		return nil
	}
	mean := ml.Index("m_mean")
	f := newForecast(len(cfg.Leads), len(mi.rows[0]))
	for li, l := range cfg.Leads {
		for pi, row := range mi.rows[li] {
			ps := b.Predict(row)
			e := row[mean]
			if math.IsNaN(float64(e)) {
				e = radar.MinDBZ
			}
			f.dbz[li][pi] = b.Apply(e, l, ps)
			f.prob[li][pi] = float32(ps[0])
		}
	}
	return &f
}

// dump writes the issue's rows (every DumpEvery-th point across and down,
// quiet rows thinned by ml.Keep) with the observations.
func (mi *mlIssue) dump(cfg Config, regionID, fold int, t int64, grid [][2]int, obs [][]float32) {
	every := max(cfg.ML.DumpEvery, 1)
	near := ml.Index("near25_max")
	mMax := ml.Index("m_max")
	var out [][]float32
	for li, l := range cfg.Leads {
		for pi, row := range mi.rows[li] {
			if grid[pi][0]%every != 0 || grid[pi][1]%every != 0 {
				continue
			}
			o := obs[li][pi]
			quiet := o < ml.QuietDBZ && !(row[near] >= ml.QuietDBZ) && !(row[mMax] >= ml.QuietDBZ)
			keep, wt := ml.Keep(quiet, regionID, t, l, pi)
			if !keep {
				continue
			}
			r := make([]float32, 0, len(ml.Leading)+len(row))
			r = append(r, float32(regionID), float32(fold), float32((t-ml.TEpoch)/60), float32(l), wt, o)
			r = append(r, row...)
			out = append(out, r)
		}
	}
	cfg.ML.Dump.Rows(out)
}
