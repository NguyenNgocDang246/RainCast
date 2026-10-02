package backtest

import (
	"math"
	"math/rand/v2"
	"slices"
	"sort"

	"raincast/internal/verify"
)

// resamples is how many bootstrap resamples the intervals use, and
// minBlocks the fewest blocks they mean anything with.
const (
	resamples = 1000
	minBlocks = 10
)

func (cfg Config) heads() []Result {
	out := []Result{{Name: "Giữ nguyên (baseline)", Method: "persistence", Baseline: true}}
	for _, v := range cfg.Variants {
		out = append(out, Result{Name: v.Name, Method: v.Method, Pairs: v.Pairs, Trend: v.Trend, Accel: v.Accel})
	}
	return out
}

// sum adds the blocks' accumulators: [variant][lead].
func sum(blocks []*block, nVar, nLead int) ([][]leadAcc, []int64, int) {
	acc := make([][]leadAcc, nVar)
	for v := range acc {
		acc[v] = make([]leadAcc, nLead)
	}
	nanos := make([]int64, nVar)
	issues := 0
	for _, b := range blocks {
		issues += b.Issues
		for v := range acc {
			for l := range acc[v] {
				acc[v][l].add(b.Acc[v][l])
			}
			nanos[v] += b.Nanos[v]
		}
	}
	return acc, nanos, issues
}

func ratio(a, b float64) *float64 {
	if b == 0 {
		return nil
	}
	v := a / b
	return &v
}

func scores(a leadAcc) verify.Scores {
	s := verify.Scores{N: int(a.H + a.M + a.F + a.C), Hits: int(a.H), Misses: int(a.M),
		FalseAlarms: int(a.F), CorrectNeg: int(a.C)}
	s.Compute()
	return s
}

// results turns accumulators into Results with per-lead and overall scores.
func results(cfg Config, acc [][]leadAcc, nanos []int64, issues int) []Result {
	out := cfg.heads()
	for v := range out {
		var all leadAcc
		for li, l := range cfg.Leads {
			a := acc[v][li]
			all.add(a)
			out[v].Leads = append(out[v].Leads, LeadScore{LeadMin: l, Scores: scores(a),
				MAEdBZ: ratio(a.AbsErr, float64(a.NErr)), Brier: ratio(a.Brier, float64(a.NBrier))})
		}
		out[v].Overall = scores(all)
		out[v].Brier = ratio(all.Brier, float64(all.NBrier))
		if issues > 0 {
			out[v].MsPerIssue = float64(nanos[v]) / 1e6 / float64(issues)
		}
	}
	for v := range out {
		if out[v].Brier != nil && out[0].Brier != nil && *out[0].Brier > 0 {
			bss := 1 - *out[v].Brier / *out[0].Brier
			out[v].BSS = &bss
		}
	}
	return out
}

// report summarizes everything in the state.
func (st *State) report(cfg Config) Report {
	nVar, nLead := len(cfg.Variants)+1, len(cfg.Leads)
	blocks := st.sortedBlocks()
	acc, nanos, issues := sum(blocks, nVar, nLead)
	rep := Report{Issues: issues, Blocks: len(blocks), Reference: cfg.Reference,
		Results: results(cfg, acc, nanos, issues), Regions: []RegionSummary{}}
	for _, b := range blocks {
		if b.Issues == 0 {
			continue
		}
		if rep.From == 0 || b.Start < rep.From {
			rep.From = b.Start
		}
		rep.To = max(rep.To, b.Start+blockSec)
	}

	// Probability: discrimination, reliability and calibrated skill.
	for v := range nVar {
		var all hist
		for h := range st.Hist {
			for l := range nLead {
				all.add(st.Hist[h][v][l])
			}
		}
		rep.Results[v].AUC = auc(all)
		rep.Results[v].Reliability = reliability(all)
		rep.Results[v].BrierCal = calibratedBrier(st.Hist, v, nLead)
	}

	st.bootstrap(cfg, blocks, rep.Results)

	// Regions and climate groups.
	byRegion := map[string][]*block{}
	byClimate := map[string][]*block{}
	for _, b := range blocks {
		byRegion[b.Region] = append(byRegion[b.Region], b)
		byClimate[b.Climate] = append(byClimate[b.Climate], b)
	}
	names := make([]string, 0, len(st.Regions))
	for name := range st.Regions {
		names = append(names, name)
	}
	sort.Strings(names)
	climateEvents := map[string]int{}
	climateRegions := map[string]int{}
	for _, name := range names {
		r := st.Regions[name]
		ev := countEvents(r.Rainy, cfg)
		rep.Events += ev
		rep.Frames += len(r.Rainy)
		rep.Skipped += r.Skipped
		rep.Points = max(rep.Points, r.Points)
		climateEvents[r.Climate] += ev
		climateRegions[r.Climate]++
		ra, _, ri := sum(byRegion[name], nVar, nLead)
		s := RegionSummary{Name: name, Climate: r.Climate, Frames: len(r.Rainy), Skipped: r.Skipped, Issues: ri, Events: ev}
		for v := range nVar {
			var all leadAcc
			for l := range nLead {
				all.add(ra[v][l])
			}
			s.CSI = append(s.CSI, scores(all).CSI)
		}
		rep.Regions = append(rep.Regions, s)
	}
	climates := make([]string, 0, len(byClimate))
	for c := range byClimate {
		climates = append(climates, c)
	}
	sort.Strings(climates)
	for _, c := range climates {
		ca, cn, ci := sum(byClimate[c], nVar, nLead)
		rep.Groups = append(rep.Groups, Group{Climate: c, Regions: climateRegions[c], Issues: ci,
			Events: climateEvents[c], Results: results(cfg, ca, cn, ci)})
	}
	rep.ErrCorr = st.errCorr(cfg)
	if cfg.Weights != nil {
		rep.WeightMembers = cfg.WeightMembers
		rep.Weights = cfg.Weights.byFold
		rep.WeightsLearned = cfg.Weights.Learned
	}
	return rep
}

// bootstrap resamples the blocks to put 95% intervals on each overall CSI
// and on its difference from the reference variant.
func (st *State) bootstrap(cfg Config, blocks []*block, res []Result) {
	if len(blocks) < minBlocks {
		return // a handful of blocks would give confident-looking nonsense
	}
	nVar := len(res)
	ref := 0
	for i, r := range res {
		if r.Name == cfg.Reference {
			ref = i
		}
	}
	// Per block and variant: hits, misses, false alarms over all leads.
	type hmf struct{ h, m, f float64 }
	tot := make([][]hmf, len(blocks))
	for bi, b := range blocks {
		tot[bi] = make([]hmf, nVar)
		for v := range nVar {
			for _, a := range b.Acc[v] {
				tot[bi][v].h += float64(a.H)
				tot[bi][v].m += float64(a.M)
				tot[bi][v].f += float64(a.F)
			}
		}
	}
	csi := func(x hmf) float64 {
		if d := x.h + x.m + x.f; d > 0 {
			return x.h / d
		}
		return 0
	}
	r := rand.New(rand.NewPCG(1, 2))
	samples := make([][]float64, nVar) // CSI per resample
	deltas := make([][]float64, nVar)
	sumv := make([]hmf, nVar)
	for range resamples {
		clear(sumv)
		for range blocks {
			b := tot[r.IntN(len(blocks))]
			for v := range nVar {
				sumv[v].h += b[v].h
				sumv[v].m += b[v].m
				sumv[v].f += b[v].f
			}
		}
		refCSI := csi(sumv[ref])
		for v := range nVar {
			c := csi(sumv[v])
			samples[v] = append(samples[v], c)
			deltas[v] = append(deltas[v], c-refCSI)
		}
	}
	for v := range nVar {
		ci := percentiles(samples[v])
		res[v].CSICI = &ci
		if v != ref && res[v].Overall.CSI != nil && res[ref].Overall.CSI != nil {
			d := *res[v].Overall.CSI - *res[ref].Overall.CSI
			dci := percentiles(deltas[v])
			res[v].DeltaCSI, res[v].DeltaCSICI = &d, &dci
		}
	}
}

func percentiles(s []float64) Interval {
	slices.Sort(s)
	at := func(q float64) float64 { return s[min(len(s)-1, int(q*float64(len(s))))] }
	return Interval{at(0.025), at(0.975)}
}

// auc is the area under the ROC curve, sweeping the threshold down through
// the probability bins.
func auc(h hist) *float64 {
	var pos, neg float64
	for b := range nBins {
		pos += float64(h.Pos[b])
		neg += float64(h.Neg[b])
	}
	if pos == 0 || neg == 0 {
		return nil
	}
	var area, tpr, fpr float64
	for b := nBins - 1; b >= 0; b-- {
		ntpr, nfpr := tpr+float64(h.Pos[b])/pos, fpr+float64(h.Neg[b])/neg
		area += (nfpr - fpr) * (tpr + ntpr) / 2
		tpr, fpr = ntpr, nfpr
	}
	return &area
}

// reliability merges the bins in pairs into ten.
func reliability(h hist) []RelBin {
	var out []RelBin
	for b := 0; b < nBins; b += 2 {
		n := h.Pos[b] + h.Neg[b] + h.Pos[b+1] + h.Neg[b+1]
		if n == 0 {
			continue
		}
		out = append(out, RelBin{
			P:    (h.SumP[b] + h.SumP[b+1]) / float64(n),
			Freq: float64(h.Pos[b]+h.Pos[b+1]) / float64(n),
			N:    n,
		})
	}
	return out
}

// calibratedBrier learns an isotonic map from forecast bin to observed
// frequency on one half of the blocks and scores the other half with it,
// both ways round, so no forecast is judged by a map it helped fit.
func calibratedBrier(hs [2][][]hist, v, nLead int) *float64 {
	var sum float64
	var n int64
	for h := range hs {
		for l := range nLead {
			c := isotonic(hs[1-h][v][l])
			test := hs[h][v][l]
			for b := range nBins {
				pos, neg := float64(test.Pos[b]), float64(test.Neg[b])
				sum += pos*(1-c[b])*(1-c[b]) + neg*c[b]*c[b]
				n += test.Pos[b] + test.Neg[b]
			}
		}
	}
	return ratio(sum, float64(n))
}

// isotonic fits a non-decreasing observed frequency over the bins by pool
// adjacent violators; empty bins take their neighbors' value, or the bin
// center when there is no data at all.
func isotonic(h hist) [nBins]float64 {
	type pool struct {
		w, y   float64
		lo, hi int
	}
	var ps []pool
	for b := range nBins {
		n := float64(h.Pos[b] + h.Neg[b])
		if n == 0 {
			continue
		}
		ps = append(ps, pool{w: n, y: float64(h.Pos[b]) / n, lo: b, hi: b})
		for len(ps) > 1 && ps[len(ps)-2].y > ps[len(ps)-1].y {
			a, c := ps[len(ps)-2], ps[len(ps)-1]
			w := a.w + c.w
			ps = append(ps[:len(ps)-2], pool{w: w, y: (a.y*a.w + c.y*c.w) / w, lo: a.lo, hi: c.hi})
		}
	}
	var out [nBins]float64
	if len(ps) == 0 {
		for b := range nBins {
			out[b] = (float64(b) + 0.5) / nBins
		}
		return out
	}
	k := 0
	for b := range nBins {
		for k < len(ps)-1 && b > ps[k].hi {
			k++
		}
		out[b] = ps[k].y
	}
	return out
}

func (st *State) errCorr(cfg Config) *ErrCorr {
	c := st.Corr
	if c.N < 2 {
		return nil
	}
	heads := cfg.heads()
	ec := &ErrCorr{Names: make([]string, len(heads)), R: make([][]*float64, len(heads))}
	n := float64(c.N)
	for v := range heads {
		ec.Names[v] = heads[v].Name
		ec.R[v] = make([]*float64, len(heads))
	}
	for v := range heads {
		for w := v; w < len(heads); w++ {
			num := n*c.Prod[v][w] - c.S[v]*c.S[w]
			den := math.Sqrt((n*c.S2[v] - c.S[v]*c.S[v]) * (n*c.S2[w] - c.S[w]*c.S[w]))
			if den > 0 {
				r := num / den
				ec.R[v][w], ec.R[w][v] = &r, &r
			}
		}
	}
	return ec
}

// countEvents counts runs of at least EventFrames consecutive rainy frames.
func countEvents(rainy map[int64]bool, cfg Config) int {
	need := max(cfg.EventFrames, 1)
	times := make([]int64, 0, len(rainy))
	for t := range rainy {
		times = append(times, t)
	}
	slices.Sort(times)
	events, run := 0, 0
	var prev int64
	for _, t := range times {
		if !rainy[t] || (run > 0 && t-prev != cfg.StepSec) {
			run = 0
		}
		if rainy[t] {
			run++
			if run == need {
				events++
			}
		}
		prev = t
	}
	return events
}
