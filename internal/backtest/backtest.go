// Package backtest re-runs the nowcast on stored radar frames with several
// model settings and scores each against the frames that actually followed.
package backtest

import (
	"math"
	"sort"
	"time"

	"raincast/internal/motion"
	"raincast/internal/nowcast"
	"raincast/internal/radar"
	"raincast/internal/verify"
)

// Frame is a stored radar frame.
type Frame struct {
	Time   int64 // unix seconds
	Mosaic *radar.Mosaic
}

// Variant is one model setting to score.
type Variant struct {
	Name  string
	Pairs int  // frame pairs averaged for motion, newest weighted most
	Trend bool // extrapolate intensity growth/decay
}

// Config controls the run.
type Config struct {
	Variants  []Variant
	Leads     []int // minutes
	Threshold float32
	Radius    int
	TrendTau  float64
	// Points are sampled every Step pixels inside the central tile, which
	// keeps a full tile of radar around each one.
	Step    int
	StepSec int64 // seconds between frames (600)
}

// LeadScore is a variant's skill at one lead time.
type LeadScore struct {
	LeadMin int           `json:"lead_min"`
	Scores  verify.Scores `json:"scores"`
	MAEdBZ  *float64      `json:"mae_dbz"` // over samples where either side had rain
}

// Result is one variant's (or the persistence baseline's) score.
type Result struct {
	Name    string        `json:"name"`
	Leads   []LeadScore   `json:"leads"`
	Overall verify.Scores `json:"overall"`
}

// Report is the outcome of a run.
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`
	Duration    string    `json:"duration"`
	Frames      int       `json:"frames"`  // frames loaded
	Skipped     int       `json:"skipped"` // frames whose tiles were pruned
	Issues      int       `json:"issues"`  // forecast times scored
	Points      int       `json:"points"`  // sample points per issue
	From        int64     `json:"from"`
	To          int64     `json:"to"`
	Results     []Result  `json:"results"` // persistence first, then variants
}

type acc struct {
	lead    map[int]*verify.Scores
	absErr  map[int]float64
	nErr    map[int]int
	overall verify.Scores
}

func newAcc(leads []int) *acc {
	a := &acc{lead: map[int]*verify.Scores{}, absErr: map[int]float64{}, nErr: map[int]int{}}
	for _, l := range leads {
		a.lead[l] = &verify.Scores{}
	}
	return a
}

func (a *acc) add(lead int, pred, obs float32, thr float32) {
	a.lead[lead].Add(pred >= thr, obs >= thr)
	a.overall.Add(pred >= thr, obs >= thr)
	if pred >= thr || obs >= thr {
		a.absErr[lead] += math.Abs(math.Max(float64(pred), 0) - math.Max(float64(obs), 0))
		a.nErr[lead]++
	}
}

func (a *acc) result(name string, leads []int) Result {
	r := Result{Name: name}
	for _, l := range leads {
		s := *a.lead[l]
		s.Compute()
		ls := LeadScore{LeadMin: l, Scores: s}
		if n := a.nErr[l]; n > 0 {
			v := a.absErr[l] / float64(n)
			ls.MAEdBZ = &v
		}
		r.Leads = append(r.Leads, ls)
	}
	r.Overall = a.overall
	r.Overall.Compute()
	return r
}

// Run scores every issue time that has enough history for the variant
// needing the most pairs and a frame at every lead, so all variants are
// compared on exactly the same cases.
func Run(frames []Frame, cfg Config) Report {
	sort.Slice(frames, func(i, j int) bool { return frames[i].Time < frames[j].Time })
	byTime := map[int64]*radar.Mosaic{}
	for _, f := range frames {
		byTime[f.Time] = f.Mosaic
	}
	maxPairs := 1
	for _, v := range cfg.Variants {
		maxPairs = max(maxPairs, v.Pairs)
	}

	// Motion for the pair ending at frame j, computed once and shared.
	pairField := map[int64]*motion.Field{}
	pair := func(t int64) *motion.Field {
		if f, ok := pairField[t]; ok {
			return f
		}
		f := motion.Estimate(byTime[t-cfg.StepSec].Grid, byTime[t].Grid, float64(cfg.StepSec)/60, motion.DefaultOptions())
		pairField[t] = f
		return f
	}

	rep := Report{}
	base := newAcc(cfg.Leads)
	accs := make([]*acc, len(cfg.Variants))
	for i := range accs {
		accs[i] = newAcc(cfg.Leads)
	}

	for _, f := range frames {
		t := f.Time
		if !complete(byTime, t, -maxPairs, 0, cfg.StepSec) || !hasLeads(byTime, t, cfg.Leads) {
			continue
		}
		cur := f.Mosaic
		lo, hi := cur.W/3, 2*cur.W/3 // central tile of the 3×3 mosaic
		var points [][2]float64
		for y := lo; y < hi; y += cfg.Step {
			for x := lo; x < hi; x += cfg.Step {
				points = append(points, [2]float64{float64(x), float64(y)})
			}
		}
		rep.Issues++
		rep.Points = len(points)
		if rep.From == 0 {
			rep.From = t
		}
		rep.To = t

		// Persistence: the echo now, everywhere in the future.
		for _, p := range points {
			now := cur.MedianInRadius(p[0], p[1], cfg.Radius)
			for _, l := range cfg.Leads {
				base.add(l, now, byTime[t+int64(l)*60].MedianInRadius(p[0], p[1], cfg.Radius), cfg.Threshold)
			}
		}

		for vi, v := range cfg.Variants {
			fields := make([]*motion.Field, v.Pairs)
			weights := make([]float64, v.Pairs)
			for k := range v.Pairs {
				fields[k] = pair(t - int64(k)*cfg.StepSec)
				weights[k] = float64(v.Pairs - k)
			}
			field := motion.WeightedAverage(fields, weights)
			opt := nowcast.Options{Horizon: cfg.Leads[len(cfg.Leads)-1], Threshold: cfg.Threshold, Radius: cfg.Radius}
			if v.Trend {
				// Growth over 20 minutes when available, following the motion.
				span := min(2, v.Pairs)
				prev := byTime[t-int64(span)*cfg.StepSec]
				opt.Trend = motion.EstimateTrend(prev.Grid, cur.Grid, field, float64(int64(span)*cfg.StepSec)/60, cfg.Threshold-5)
				opt.TrendTau = cfg.TrendTau
			}
			for _, p := range points {
				res := nowcast.Forecast(cur.Grid, field, p[0], p[1], opt)
				for _, l := range cfg.Leads {
					accs[vi].add(l, res.At(l), byTime[t+int64(l)*60].MedianInRadius(p[0], p[1], cfg.Radius), cfg.Threshold)
				}
			}
		}
	}

	rep.Results = append(rep.Results, base.result("Giữ nguyên (baseline)", cfg.Leads))
	for i, v := range cfg.Variants {
		rep.Results = append(rep.Results, accs[i].result(v.Name, cfg.Leads))
	}
	return rep
}

// complete reports whether frames exist every step from t+from to t+to steps.
func complete(byTime map[int64]*radar.Mosaic, t int64, from, to int, step int64) bool {
	for k := from; k <= to; k++ {
		if byTime[t+int64(k)*step] == nil {
			return false
		}
	}
	return true
}

func hasLeads(byTime map[int64]*radar.Mosaic, t int64, leads []int) bool {
	for _, l := range leads {
		if byTime[t+int64(l)*60] == nil {
			return false
		}
	}
	return true
}
