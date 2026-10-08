package backtest

import (
	"math"
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
		out = append(out, Result{Name: v.Name, Method: v.Method, Pairs: v.Pairs, Trend: v.Trend})
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
				MAEdBZ: ratio(a.AbsErr, float64(a.NErr)), Brier: ratio(a.Brier, float64(a.NBrier)),
				AccumMAE: ratio(a.AccumErr, float64(a.NAccum))})
		}
		if n := len(out[v].Leads); n > 0 {
			out[v].AccumMAE = out[v].Leads[n-1].AccumMAE
		}
		out[v].Overall = scores(all)
		csi := func(i int) *float64 { return out[v].Leads[i].Scores.CSI }
		out[v].CSIMean60 = meanCSI(cfg.Leads, csi, 60)
		out[v].CSIMean90 = meanCSI(cfg.Leads, csi, 90)
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
	classResults(cfg, blocks, rep.Results)
	fssResults(cfg, blocks, rep.Results)

	rep.Comparisons = st.compare(cfg, blocks, rep.Results)

	// Regions and groups.
	byRegion := map[string][]*block{}
	for _, b := range blocks {
		byRegion[b.Region] = append(byRegion[b.Region], b)
	}
	names := make([]string, 0, len(st.Regions))
	for name := range st.Regions {
		names = append(names, name)
	}
	sort.Strings(names)
	events := map[string]int{}
	for _, name := range names {
		r := st.Regions[name]
		ev := countEvents(r.Rainy, cfg)
		rep.Events += ev
		rep.Frames += len(r.Rainy)
		rep.Skipped += r.Skipped
		rep.Points = max(rep.Points, r.Points)
		events[name] = ev
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
	for _, g := range st.groupKeys() {
		var gb []*block
		for _, b := range blocks {
			if g.in(b.Climate, b.Sat) {
				gb = append(gb, b)
			}
		}
		if len(gb) == 0 {
			continue
		}
		grp := Group{Climate: g.key}
		for _, name := range names {
			if r := st.Regions[name]; g.in(r.Climate, r.Sat) {
				grp.Regions++
				grp.Events += events[name]
			}
		}
		ga, gn, gi := sum(gb, nVar, nLead)
		grp.Issues = gi
		grp.Results = results(cfg, ga, gn, gi)
		classResults(cfg, gb, grp.Results)
		st.bootstrap(cfg, gb, grp.Results)
		grp.Comparisons = st.compare(cfg, gb, grp.Results)
		rep.Groups = append(rep.Groups, grp)
	}
	rep.ErrCorr = st.errCorr(cfg)
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
	samples, deltas := resampleCSI(tot, ref)
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

// groupDef selects the regions of a Group.
type groupDef struct {
	key string
	in  func(climate string, sat bool) bool
}

// groupKeys are the climate groups, sorted, then — when some regions are
// seen by Himawari — the satellite split: seen, not seen, and tropical and
// seen (the closest to Vietnam).
func (st *State) groupKeys() []groupDef {
	climates := map[string]bool{}
	anySat := false
	for _, r := range st.Regions {
		climates[r.Climate] = true
		anySat = anySat || r.Sat
	}
	keys := make([]string, 0, len(climates))
	for c := range climates {
		keys = append(keys, c)
	}
	sort.Strings(keys)
	var out []groupDef
	for _, c := range keys {
		out = append(out, groupDef{c, func(cl string, _ bool) bool { return cl == c }})
	}
	if anySat {
		out = append(out,
			groupDef{"sat", func(_ string, s bool) bool { return s }},
			groupDef{"nosat", func(_ string, s bool) bool { return !s }},
			groupDef{"tropical_sat", func(cl string, s bool) bool { return s && cl == "tropical" }})
	}
	return out
}

// compare scores Config.Compare over blocks: the difference in CSI pooled
// over the leads up to CompareLead, with a block-resampled interval.
func (st *State) compare(cfg Config, blocks []*block, res []Result) []Comparison {
	if len(cfg.Compare) == 0 {
		return nil
	}
	idx := map[string]int{}
	for i, r := range res {
		idx[r.Name] = i
	}
	nl, last := 0, 0
	for li, l := range cfg.Leads {
		if l <= CompareLead {
			nl, last = li+1, l
		}
	}
	var out []Comparison
	for _, pair := range cfg.Compare {
		a, okA := idx[pair[0]]
		b, okB := idx[pair[1]]
		if !okA || !okB || nl == 0 {
			continue
		}
		tot := make([][]hmf, len(blocks))
		var all [2]hmf
		for bi, bl := range blocks {
			tot[bi] = make([]hmf, 2)
			for k, v := range [2]int{a, b} {
				for _, acc := range bl.Acc[v][:nl] {
					tot[bi][k].h += float64(acc.H)
					tot[bi][k].m += float64(acc.M)
					tot[bi][k].f += float64(acc.F)
				}
				all[k].h += tot[bi][k].h
				all[k].m += tot[bi][k].m
				all[k].f += tot[bi][k].f
			}
		}
		c := Comparison{Base: pair[0], Other: pair[1], MaxLead: last}
		if all[0].h+all[0].m+all[0].f > 0 && all[1].h+all[1].m+all[1].f > 0 {
			d := csiOf(all[1]) - csiOf(all[0])
			c.Delta = &d
			if _, deltas := resampleCSI(tot, 0); deltas != nil {
				ci := percentiles(deltas[1])
				c.CI = &ci
			}
		}
		out = append(out, c)
	}
	return out
}
