package backtest

import (
	"context"
	"math"
	"time"

	"raincast/internal/himawari"
	"raincast/internal/ml"
	"raincast/internal/motion"
	"raincast/internal/nowcast"
	"raincast/internal/radar"
)

// corrLead is the lead time error correlations are measured at.
const corrLead = 30

// forecast is one variant's prediction at every point and lead.
type forecast struct {
	dbz  [][]float32 // [lead][point]
	prob [][]float32
}

func newForecast(nLead, nPoint int) forecast {
	f := forecast{dbz: make([][]float32, nLead), prob: make([][]float32, nLead)}
	for l := range nLead {
		f.dbz[l] = make([]float32, nPoint)
		f.prob[l] = make([]float32, nPoint)
	}
	return f
}

// scoreRegion scores every issue time after last that has enough history
// for the variant needing the most pairs and a frame at every lead, so all
// variants are compared on exactly the same cases.
func scoreRegion(ctx context.Context, reg Region, cfg Config, last int64) (*State, error) {
	st := NewState(cfg)
	sat := reg.Lat != 0 && himawari.Covers(reg.Lat, reg.Lon)
	rs := &regionState{Climate: reg.Climate, Sat: sat, Last: last, Rainy: map[int64]bool{}}
	st.Regions[reg.Name] = rs
	maxPairs := 1
	for _, v := range cfg.Variants {
		maxPairs = max(maxPairs, v.Pairs)
	}
	w := newWindow(reg.Source, reg.Coverage, cfg)
	nVar := len(cfg.Variants) + 1
	byName := map[string]int{}
	for i, v := range cfg.Variants {
		byName[v.Name] = i + 1
	}
	horizon := cfg.Leads[len(cfg.Leads)-1]
	isLead := map[int]bool{}
	corrIdx := -1
	for i, l := range cfg.Leads {
		isLead[l] = true
		if l == corrLead {
			corrIdx = i
		}
	}
	var members []int
	mlRegion, mlFold := -1, ml.Fold(reg.Lat, reg.Lon)
	if cfg.mlNeeded() {
		for _, m := range ml.Members {
			idx := -1
			if n := cfg.memberName(m); n != "" {
				idx = byName[n]
			}
			members = append(members, idx)
		}
		if cfg.ML.Dump != nil {
			mlRegion = cfg.ML.Dump.Region(ml.RegionInfo{Name: reg.Name, Climate: reg.Climate, Lat: reg.Lat, Lon: reg.Lon,
				Fold: mlFold, Sat: himawari.Covers(reg.Lat, reg.Lon)})
		}
	}
	probRadius := func(m int) int {
		if isLead[m] {
			return nowcast.DefaultProbRadius(m)
		}
		return -1
	}

	for _, t := range reg.Source.Times() {
		if t <= last || t < cfg.Since {
			continue
		}
		if cfg.done != nil {
			cfg.done.Add(1)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		w.evict(t - int64(maxPairs+1)*cfg.StepSec)
		if !w.complete(t, -maxPairs, 0) || !w.hasLeads(t) {
			continue
		}
		cur := w.grid(t)
		lo, hi := cur.W/3, 2*cur.W/3 // central tile of the 3×3 mosaic
		var points [][2]float64
		var grid [][2]int // lattice position of each point
		var lat lattice
		for y := lo; y < hi; y += cfg.Step {
			for x := lo; x < hi; x += cfg.Step {
				if reg.Coverage.At(x, y) {
					points = append(points, [2]float64{float64(x), float64(y)})
					grid = append(grid, [2]int{(x - lo) / cfg.Step, (y - lo) / cfg.Step})
					lat.put((x-lo)/cfg.Step, (y-lo)/cfg.Step)
				}
			}
		}
		if len(points) == 0 {
			continue
		}
		rs.Last = t
		rs.Points = max(rs.Points, len(points))
		b := st.block(reg.Name, reg.Climate, t, nVar, len(cfg.Classes), len(cfg.Leads))
		b.Issues++
		b.Sat = sat
		hh := st.Hist[half(t)]

		obs := make([][]float32, len(cfg.Leads))
		obsDry := true
		for li, l := range cfg.Leads {
			fut := w.grid(t + int64(l)*60)
			obs[li] = make([]float32, len(points))
			for pi, p := range points {
				obs[li][pi] = fut.PointEcho(p[0], p[1], cfg.Radius, cfg.Strong)
				obsDry = obsDry && obs[li][pi] < cfg.Threshold
			}
		}
		// With no echo anywhere in the mosaic, no method can forecast rain
		// (advection only moves echoes, trend only changes existing ones):
		// if none arrived either, every forecast is a dry, certain, correct
		// negative and need not be computed.
		if obsDry && maxDBZ(cur.Grid) < 10 {
			n := int64(len(points))
			for v := range nVar {
				for li := range cfg.Leads {
					b.Acc[v][li].C += n
					b.Acc[v][li].NBrier += n
					hh[v][li].Neg[0] += n
				}
			}
			continue
		}

		fc := make([]forecast, nVar)
		base := nowcast.Options{Horizon: horizon, Threshold: cfg.Threshold, Radius: cfg.Radius, Strong: cfg.Strong, ProbRadius: probRadius}
		// run forecasts with variant v's motion and upgrades; persistence is
		// the zero Variant, which has no motion.
		run := func(i int, v Variant) {
			start := time.Now()
			f := newForecast(len(cfg.Leads), len(points))
			opt := base
			var field *motion.Field
			if v.Method != "" {
				field = w.field(v.Method, t, v.Pairs)
			}
			if field != nil && v.Trend {
				opt.Trend = w.trend(t, v.Method, v.Pairs)
				opt.TrendTau = cfg.TrendTau
			}
			for pi, p := range points {
				res := nowcast.Forecast(cur.Grid, field, p[0], p[1], opt)
				for li, l := range cfg.Leads {
					f.dbz[li][pi] = res.At(l)
					f.prob[li][pi] = res.ProbAt(l)
				}
			}
			fc[i] = f
			b.Nanos[i] += int64(time.Since(start))
		}
		// Persistence: the echo now, everywhere in the future.
		run(0, Variant{})
		var mi *mlIssue
		mlRows := func() *mlIssue {
			if mi == nil {
				mi = buildML(w, reg, cfg, t, cur, points, fc, members)
			}
			return mi
		}
		for vi, v := range cfg.Variants {
			i := vi + 1
			switch v.Method {
			case MethodMean:
				start := time.Now()
				fc[i] = combine(fc, v, byName, len(cfg.Leads), len(points))
				b.Nanos[i] += int64(time.Since(start))
			case MethodML:
				start := time.Now()
				if f := mlRows().forecast(cfg, v, mlFold); f != nil {
					fc[i] = *f
				} else {
					fc[i] = newForecast(len(cfg.Leads), len(points))
				}
				b.Nanos[i] += int64(time.Since(start))
			default:
				run(i, v)
			}
		}
		if cfg.ML != nil && cfg.ML.Dump != nil {
			mlRows().dump(cfg, mlRegion, mlFold, t, grid, obs)
		}

		addAccum(b.Acc, fc, obs, cfg.Leads)
		if len(cfg.FSSThresholds) > 0 && len(cfg.FSSWindows) > 0 {
			addFSS(b.fssAccs(nVar, len(cfg.FSSThresholds), len(cfg.FSSWindows), len(cfg.Leads)), fc, obs, &lat, cfg)
		}
		for v := range nVar {
			for li := range cfg.Leads {
				a := &b.Acc[v][li]
				hv := &hh[v][li]
				for pi := range points {
					pred, o := fc[v].dbz[li][pi], obs[li][pi]
					pr, or := pred >= cfg.Threshold, o >= cfg.Threshold
					switch {
					case pr && or:
						a.H++
					case or:
						a.M++
					case pr:
						a.F++
					default:
						a.C++
					}
					for k, c := range cfg.Classes {
						b.Cls[v][k][li].score(c.in(pred), c.in(o))
					}
					if pr || or {
						a.AbsErr += math.Abs(math.Max(float64(pred), 0) - math.Max(float64(o), 0))
						a.NErr++
					}
					p := fc[v].prob[li][pi]
					ov := float32(0)
					if or {
						ov = 1
					}
					a.Brier += float64((p - ov) * (p - ov))
					a.NBrier++
					bin := binOf(p)
					if or {
						hv.Pos[bin]++
					} else {
						hv.Neg[bin]++
					}
					hv.SumP[bin] += float64(p)
				}
			}
		}
		if corrIdx >= 0 {
			addCorr(&st.Corr, fc, obs[corrIdx], corrIdx, cfg.Threshold)
		}
	}
	for t, r := range w.rainy {
		rs.Rainy[t] = r
	}
	rs.Skipped = w.skipped
	return st, nil
}

// addAccum scores the rain each variant accumulates up to every lead
// against what radar showed, each lead standing for the minutes since the
// one before.
func addAccum(acc [][]leadAcc, fc []forecast, obs [][]float32, leads []int) {
	for v := range fc {
		for p := range obs[0] {
			var pred, seen float64
			prev := 0
			for li, l := range leads {
				h := float64(l-prev) / 60
				prev = l
				pred += radar.RainRate(fc[v].dbz[li][p]) * h
				seen += radar.RainRate(obs[li][p]) * h
				if pred > 0 || seen > 0 {
					acc[v][li].AccumErr += math.Abs(pred - seen)
					acc[v][li].NAccum++
				}
			}
		}
	}
}

// combine averages the members' dBZ and rain probability (MethodMean).
func combine(fc []forecast, v Variant, byName map[string]int, nLead, nPoint int) forecast {
	out := newForecast(nLead, nPoint)
	n := float32(len(v.Members))
	for _, m := range v.Members {
		f := fc[byName[m]]
		for l := range nLead {
			for p := range nPoint {
				out.dbz[l][p] += f.dbz[l][p] / n
				out.prob[l][p] += f.prob[l][p] / n
			}
		}
	}
	return out
}

// addCorr accumulates dBZ errors at one lead where the observation or any
// forecast has rain.
func addCorr(c *corrAcc, fc []forecast, obs []float32, li int, thr float32) {
	e := make([]float64, len(fc))
	for p, o := range obs {
		rain := o >= thr
		for v := range fc {
			rain = rain || fc[v].dbz[li][p] >= thr
		}
		if !rain {
			continue
		}
		for v := range fc {
			e[v] = math.Max(float64(fc[v].dbz[li][p]), 0) - math.Max(float64(o), 0)
		}
		c.N++
		for v := range e {
			c.S[v] += e[v]
			c.S2[v] += e[v] * e[v]
			for w := v; w < len(e); w++ {
				c.Prod[v][w] += e[v] * e[w]
			}
		}
	}
}

func maxDBZ(g *radar.Grid) float32 {
	m := radar.MinDBZ
	for _, v := range g.Data {
		m = max(m, v)
	}
	return m
}
