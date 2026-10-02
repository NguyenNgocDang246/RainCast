package backtest

import (
	"context"
	"math"
	"time"

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
	rs := &regionState{Climate: reg.Climate, Last: last, Rainy: map[int64]bool{}}
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
	var memberIdx []int
	for _, m := range cfg.WeightMembers {
		if i, ok := byName[m]; ok {
			memberIdx = append(memberIdx, i)
		}
	}
	if len(memberIdx) != len(cfg.WeightMembers) {
		memberIdx = nil
	}
	probRadius := func(m int) int {
		if isLead[m] {
			return nowcast.DefaultProbRadius(m)
		}
		return -1
	}

	for _, t := range reg.Source.Times() {
		if t <= last {
			continue
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
		for y := lo; y < hi; y += cfg.Step {
			for x := lo; x < hi; x += cfg.Step {
				if reg.Coverage.At(x, y) {
					points = append(points, [2]float64{float64(x), float64(y)})
				}
			}
		}
		if len(points) == 0 {
			continue
		}
		rs.Last = t
		rs.Points = max(rs.Points, len(points))
		b := st.block(reg.Name, reg.Climate, t, nVar, len(cfg.Leads))
		b.Issues++
		hh := st.Hist[half(t)]

		obs := make([][]float32, len(cfg.Leads))
		obsDry := true
		for li, l := range cfg.Leads {
			fut := w.grid(t + int64(l)*60)
			obs[li] = make([]float32, len(points))
			for pi, p := range points {
				obs[li][pi] = fut.MedianInRadius(p[0], p[1], cfg.Radius)
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
		base := nowcast.Options{Horizon: horizon, Threshold: cfg.Threshold, Radius: cfg.Radius, ProbRadius: probRadius}
		run := func(v int, field func() nowcastField, trend bool, accel func() *motion.Field) {
			start := time.Now()
			f := newForecast(len(cfg.Leads), len(points))
			opt := base
			fld := field()
			if trend && fld.field != nil {
				opt.Trend = w.trend(t, fld.field)
				opt.TrendTau = cfg.TrendTau
			}
			if accel != nil && fld.field != nil {
				opt.Accel = accel()
			}
			for pi, p := range points {
				res := nowcast.Forecast(cur.Grid, fld.field, p[0], p[1], opt)
				for li, l := range cfg.Leads {
					f.dbz[li][pi] = res.At(l)
					f.prob[li][pi] = res.ProbAt(l)
				}
			}
			fc[v] = f
			b.Nanos[v] += int64(time.Since(start))
		}
		// Persistence: the echo now, everywhere in the future.
		run(0, func() nowcastField { return nowcastField{} }, false, nil)
		for vi, v := range cfg.Variants {
			i := vi + 1
			switch v.Method {
			case MethodMean, MethodVote:
				start := time.Now()
				fc[i] = combine(fc, v, byName, cfg.Threshold, len(cfg.Leads), len(points))
				b.Nanos[i] += int64(time.Since(start))
			case MethodWeighted:
				start := time.Now()
				fc[i] = combineWeighted(fc, v, byName, reg.Name, cfg.Weights, len(cfg.Leads), len(points))
				b.Nanos[i] += int64(time.Since(start))
			default:
				var accel func() *motion.Field
				if v.Accel {
					accel = func() *motion.Field { return w.accel(v.Method, t, v.Pairs) }
				}
				run(i, func() nowcastField { return nowcastField{w.field(v.Method, t, v.Pairs)} }, v.Trend, accel)
			}
		}

		addAccum(b.Acc, fc, obs, cfg.Leads)
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
		if len(memberIdx) > 0 {
			addMemberStats(st.WStats.region(reg.Name), fc, memberIdx, obs, cfg.Threshold)
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

// nowcastField wraps a possibly nil field (persistence has none).
type nowcastField struct{ field *motion.Field }

// combine averages the members' forecasts (MethodMean), or turns their
// rain/no-rain answers into a probability (MethodVote, with the mean dBZ).
func combine(fc []forecast, v Variant, byName map[string]int, thr float32, nLead, nPoint int) forecast {
	out := newForecast(nLead, nPoint)
	n := float32(len(v.Members))
	for _, m := range v.Members {
		f := fc[byName[m]]
		for l := range nLead {
			for p := range nPoint {
				out.dbz[l][p] += f.dbz[l][p] / n
				if v.Method == MethodVote {
					if f.dbz[l][p] >= thr {
						out.prob[l][p] += 1 / n
					}
				} else {
					out.prob[l][p] += f.prob[l][p] / n
				}
			}
		}
	}
	return out
}

// combineWeighted mixes the members' forecasts with the weights learned for
// the region's fold at each lead.
func combineWeighted(fc []forecast, v Variant, byName map[string]int, region string, w *Weights, nLead, nPoint int) forecast {
	out := newForecast(nLead, nPoint)
	for l := range nLead {
		ws := w.For(region, v.Weighting, l, len(v.Members))
		if v.Weighting == WeightEqual || len(ws) != len(v.Members) {
			ws = equal(len(v.Members))
		}
		for mi, m := range v.Members {
			f := fc[byName[m]]
			wt := float32(ws[mi])
			for p := range nPoint {
				out.dbz[l][p] += wt * f.dbz[l][p]
				out.prob[l][p] += wt * f.prob[l][p]
			}
		}
	}
	return out
}

// addMemberStats accumulates what weights are learned from: each member's
// and persistence's hits, misses and false alarms, and the least-squares
// sums of observed on forecast dBZ.
func addMemberStats(ms []*memberStats, fc []forecast, idx []int, obs [][]float32, thr float32) {
	x := make([]float64, len(idx))
	for l, s := range ms {
		for p, o := range obs[l] {
			or := o >= thr
			count := func(k int, pred float32) {
				pr := pred >= thr
				switch {
				case pr && or:
					s.H[k]++
				case or:
					s.M[k]++
				case pr:
					s.F[k]++
				}
			}
			for k, v := range idx {
				count(k, fc[v].dbz[l][p])
				x[k] = math.Max(float64(fc[v].dbz[l][p]), 0)
			}
			count(len(idx), fc[0].dbz[l][p])
			// Only samples with rain somewhere teach the regression anything.
			y := math.Max(float64(o), 0)
			if y == 0 && slicesMax(x) == 0 {
				continue
			}
			for i := range x {
				for j := range x {
					s.XtX[i][j] += x[i] * x[j]
				}
				s.Xty[i] += x[i] * y
			}
			s.N++
		}
	}
}

func slicesMax(s []float64) float64 {
	m := math.Inf(-1)
	for _, v := range s {
		m = math.Max(m, v)
	}
	return m
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
