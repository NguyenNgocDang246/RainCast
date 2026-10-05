package backtest

import (
	"fmt"
	"math/rand/v2"

	"raincast/internal/radar"
	"raincast/internal/verify"
)

// Class is a kind of rain by echo strength: Lo ≤ dBZ < Hi (no upper bound
// when Hi is 0). A forecast is scored on a class as a yes/no question —
// will the point be in this class? — so a light-rain forecast that turns
// out heavy is a miss for heavy rain and a false alarm for light rain.
type Class struct {
	Name string  `json:"name"`
	Lo   float32 `json:"lo"`
	Hi   float32 `json:"hi,omitempty"`
}

func (c Class) in(v float32) bool { return v >= c.Lo && (c.Hi == 0 || v < c.Hi) }

// DefaultClasses follow rain rates through radar.RainRate: light below
// ~2.7 mm/h, moderate to ~11.5, heavy to ~50, very heavy beyond; then
// thresholds, moderate or worse and heavy or worse, which only ask whether
// the rain reaches a strength (what warnings are about), so a forecast one
// band too strong or weak above the threshold still counts.
func DefaultClasses() []Class {
	return []Class{
		{Name: "Mưa nhẹ (20–30 dBZ)", Lo: 20, Hi: 30},
		{Name: "Mưa vừa (30–40 dBZ)", Lo: 30, Hi: 40},
		{Name: "Mưa to (40–50 dBZ)", Lo: 40, Hi: 50},
		{Name: "Mưa rất to (≥ 50 dBZ)", Lo: 50},
		{Name: "Từ mưa vừa trở lên (≥ 30 dBZ)", Lo: 30},
		{Name: "Từ mưa to trở lên (≥ 40 dBZ)", Lo: 40},
	}
}

// PaperClasses are thresholds only, the ones published nowcasting
// verifications use, so the scores compare with them: rain rates in mm/h
// (RainNet: 1, 5, 10, 15; DGMR: 1, 4, 8) turned into dBZ through the same
// Z–R relation as radar.RainRate, and reflectivity thresholds (≥ 20, 30,
// 40 dBZ).
func PaperClasses() []Class {
	var cs []Class
	for _, r := range []float64{1, 4, 5, 8, 10, 15} {
		cs = append(cs, Class{Name: fmt.Sprintf("≥ %g mm/h (%.1f dBZ)", r, radar.DBZ(r)), Lo: radar.DBZ(r)})
	}
	for _, d := range []float32{20, 30, 40} {
		cs = append(cs, Class{Name: fmt.Sprintf("≥ %g dBZ", d), Lo: d})
	}
	return cs
}

// meanCSI is the plain mean of the CSIs at leads up to upTo minutes, how
// papers summarize a nowcast over its horizon (pooling hits instead weights
// the short leads, which have more rain both sides); nil when a lead in
// range has no CSI or the leads stop short of upTo.
func meanCSI(leads []int, csi func(i int) *float64, upTo int) *float64 {
	var s float64
	n := 0
	reached := false
	for i, l := range leads {
		if l > upTo {
			break
		}
		c := csi(i)
		if c == nil {
			return nil
		}
		s += *c
		n++
		reached = l == upTo
	}
	if !reached {
		return nil
	}
	m := s / float64(n)
	return &m
}

// classAcc counts one variant on one class at one lead.
type classAcc struct{ H, M, F int64 }

func (a *classAcc) add(o classAcc) {
	a.H += o.H
	a.M += o.M
	a.F += o.F
}

func (a *classAcc) score(pred, obs bool) {
	switch {
	case pred && obs:
		a.H++
	case obs:
		a.M++
	case pred:
		a.F++
	}
}

// ClassScore is a result's skill on one class.
type ClassScore struct {
	Class string  `json:"class"`
	Lo    float32 `json:"lo"`
	Hi    float32 `json:"hi,omitempty"`
	// Observed is how many samples the class was observed in: few means
	// the scores are noisy.
	Observed int64         `json:"observed"`
	Leads    []ClassLead   `json:"leads"`
	Overall  verify.Scores `json:"overall"`
	// CSIMean60 and CSIMean90 are meanCSI up to 60 and 90 minutes.
	CSIMean60  *float64  `json:"csi_mean_60,omitempty"`
	CSIMean90  *float64  `json:"csi_mean_90,omitempty"`
	CSICI      *Interval `json:"csi_ci,omitempty"`
	DeltaCSI   *float64  `json:"delta_csi,omitempty"`
	DeltaCSICI *Interval `json:"delta_csi_ci,omitempty"`
}

// ClassLead is a class's scores at one lead.
type ClassLead struct {
	LeadMin int           `json:"lead_min"`
	Scores  verify.Scores `json:"scores"`
}

func classScores(a classAcc) verify.Scores {
	s := verify.Scores{N: int(a.H + a.M + a.F), Hits: int(a.H), Misses: int(a.M), FalseAlarms: int(a.F)}
	s.Compute()
	return s
}

// classResults adds every class's scores, with bootstrap intervals over the
// blocks, to res (one per variant).
func classResults(cfg Config, blocks []*block, res []Result) {
	nVar, nCls := len(res), len(cfg.Classes)
	if nCls == 0 {
		return
	}
	ref := 0
	for i, r := range res {
		if r.Name == cfg.Reference {
			ref = i
		}
	}
	for k, c := range cfg.Classes {
		// Per block and variant, over all leads, for the intervals.
		tot := make([][]hmf, 0, len(blocks))
		sums := make([][]classAcc, nVar) // [variant][lead]
		for v := range sums {
			sums[v] = make([]classAcc, len(cfg.Leads))
		}
		for _, b := range blocks {
			if len(b.Cls) != nVar {
				continue
			}
			row := make([]hmf, nVar)
			for v := range nVar {
				for l, a := range b.Cls[v][k] {
					sums[v][l].add(a)
					row[v].h += float64(a.H)
					row[v].m += float64(a.M)
					row[v].f += float64(a.F)
				}
			}
			tot = append(tot, row)
		}
		samples, deltas := resampleCSI(tot, ref)
		for v := range nVar {
			cs := ClassScore{Class: c.Name, Lo: c.Lo, Hi: c.Hi}
			var all classAcc
			for l, lead := range cfg.Leads {
				a := sums[v][l]
				all.add(a)
				cs.Leads = append(cs.Leads, ClassLead{LeadMin: lead, Scores: classScores(a)})
			}
			cs.Observed = all.H + all.M
			cs.Overall = classScores(all)
			csi := func(i int) *float64 { return cs.Leads[i].Scores.CSI }
			cs.CSIMean60 = meanCSI(cfg.Leads, csi, 60)
			cs.CSIMean90 = meanCSI(cfg.Leads, csi, 90)
			if samples != nil {
				ci := percentiles(samples[v])
				cs.CSICI = &ci
			}
			res[v].Classes = append(res[v].Classes, cs)
		}
		// Differences from the reference, now that it has its score too.
		if samples != nil {
			refCS := res[ref].Classes[k]
			for v := range nVar {
				cs := &res[v].Classes[k]
				if v != ref && cs.Overall.CSI != nil && refCS.Overall.CSI != nil {
					d := *cs.Overall.CSI - *refCS.Overall.CSI
					dci := percentiles(deltas[v])
					cs.DeltaCSI, cs.DeltaCSICI = &d, &dci
				}
			}
		}
	}
}

// hmf are hits, misses and false alarms of one variant in one block.
type hmf struct{ h, m, f float64 }

func csiOf(x hmf) float64 {
	if d := x.h + x.m + x.f; d > 0 {
		return x.h / d
	}
	return 0
}

// resampleCSI draws blocks with replacement and returns, per variant, the
// CSI of every resample and its difference from variant ref's; nil with
// too few blocks to mean anything.
func resampleCSI(tot [][]hmf, ref int) (samples, deltas [][]float64) {
	if len(tot) < minBlocks {
		return nil, nil
	}
	nVar := len(tot[0])
	r := rand.New(rand.NewPCG(1, 2))
	samples = make([][]float64, nVar)
	deltas = make([][]float64, nVar)
	sumv := make([]hmf, nVar)
	for range resamples {
		clear(sumv)
		for range tot {
			b := tot[r.IntN(len(tot))]
			for v := range nVar {
				sumv[v].h += b[v].h
				sumv[v].m += b[v].m
				sumv[v].f += b[v].f
			}
		}
		refCSI := csiOf(sumv[ref])
		for v := range nVar {
			c := csiOf(sumv[v])
			samples[v] = append(samples[v], c)
			deltas[v] = append(deltas[v], c-refCSI)
		}
	}
	return samples, deltas
}
