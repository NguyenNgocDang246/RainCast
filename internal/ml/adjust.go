package ml

import (
	"math"

	"raincast/internal/nowcast"
	"raincast/internal/radar"
)

// Leads are the minutes ahead the models were trained and scored at
// (cmd/backtest's leads).
var Leads = []int{10, 20, 30, 40, 50, 60}

// LeadProbRadius is the nowcast.Options.ProbRadius the members' rain
// probability is read with: nowcast.DefaultProbRadius at Leads, as the
// backtest computes it, and none in between.
func LeadProbRadius(m int) int {
	for _, l := range Leads {
		if m == l {
			return nowcast.DefaultProbRadius(m)
		}
	}
	return -1
}

// Prime computes what Prepare shares between points. After it a Scene may
// be copied (to set a point's Lat, Lon and KmPerPx) and the copies used
// concurrently.
func (s *Scene) Prime() {
	if s.mean == nil {
		s.mean = meanField(s.Fields)
	}
}

// Adjust post-processes series, the members' mean echo per minute at pixel
// (x, y) of s, the way the backtest scores the ML variant: at each of Leads
// the echo moves into the band b's probabilities point to (Apply), and
// between them the change is interpolated, from none now. members are the
// members' own trended nowcasts in Members order (a zero Result for a
// missing one), read with LeadProbRadius. It reports whether it applied,
// false when no member forecast the point.
func (b *Bundle) Adjust(s *Scene, x, y float64, members []nowcast.Result, series []nowcast.Point) bool {
	pt := s.Prepare(x, y, Leads)
	row := make([]float32, len(Names))
	vals := make([]float32, len(members))
	mean := Index("m_mean")
	delta := make([]float64, len(Leads))
	for li, l := range Leads {
		var prob float32
		n := 0
		for k, r := range members {
			vals[k] = float32(math.NaN())
			if len(r.Series) > 0 {
				vals[k] = r.At(l)
				prob += r.ProbAt(l)
				n++
			}
		}
		if n == 0 {
			return false
		}
		s.Row(pt, li, l, vals, prob/float32(n), row)
		e := row[mean]
		delta[li] = float64(b.Apply(e, l, b.Predict(row)) - e)
	}
	for i := range series {
		if d := leadDelta(delta, series[i].Minute); d != 0 {
			series[i].DBZ = max(series[i].DBZ+float32(d), radar.MinDBZ)
		}
	}
	return true
}

// leadDelta interpolates the change at Leads (delta) to minute m: none at
// minute 0, the last lead's beyond it.
func leadDelta(delta []float64, m int) float64 {
	prevM, prevD := 0, 0.0
	for li, l := range Leads {
		if m <= l {
			w := float64(m-prevM) / float64(l-prevM)
			return prevD + (delta[li]-prevD)*w
		}
		prevM, prevD = l, delta[li]
	}
	return prevD
}
