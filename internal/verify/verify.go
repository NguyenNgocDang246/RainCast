// Package verify scores past forecasts against the radar frames that later
// arrived for the same time.
package verify

import (
	"math"
	"sort"
)

// Row is one forecast for one lead time, with its observed outcome.
type Row struct {
	IssuedAt    int64
	LeadMin     int
	PredRain    bool
	PersistRain bool // baseline: "the weather stays as it is now"
	ObsRain     bool
	PredDBZ     float64
	ObsDBZ      float64
	// Trend is the shadow forecast with intensity growth/decay, when it
	// was recorded.
	Trend *Prediction
}

// Prediction is one predicted value at one lead.
type Prediction struct {
	DBZ  float64
	Rain bool
}

// Scores is a 2×2 contingency table and the usual skill scores derived from
// it. A score is nil when its denominator is zero.
type Scores struct {
	N           int      `json:"n"`
	Hits        int      `json:"hits"`
	Misses      int      `json:"misses"`
	FalseAlarms int      `json:"false_alarms"`
	CorrectNeg  int      `json:"correct_negatives"`
	Accuracy    *float64 `json:"accuracy"`
	POD         *float64 `json:"pod"` // probability of detection
	FAR         *float64 `json:"far"` // false alarm ratio
	CSI         *float64 `json:"csi"` // critical success index
}

// Add records one forecast/observation pair.
func (s *Scores) Add(pred, obs bool) {
	s.N++
	switch {
	case pred && obs:
		s.Hits++
	case !pred && obs:
		s.Misses++
	case pred && !obs:
		s.FalseAlarms++
	default:
		s.CorrectNeg++
	}
}

// Compute fills the derived scores from the counts.
func (s *Scores) Compute() {
	s.Accuracy = ratio(s.Hits+s.CorrectNeg, s.N)
	s.POD = ratio(s.Hits, s.Hits+s.Misses)
	s.FAR = ratio(s.FalseAlarms, s.Hits+s.FalseAlarms)
	s.CSI = ratio(s.Hits, s.Hits+s.Misses+s.FalseAlarms)
}

func ratio(a, b int) *float64 {
	if b == 0 {
		return nil
	}
	v := float64(a) / float64(b)
	return &v
}

// LeadStats compares the model against persistence for one lead time.
type LeadStats struct {
	LeadMin     int      `json:"lead_min"`
	Model       Scores   `json:"model"`
	Persistence Scores   `json:"persistence"`
	MAEdBZ      *float64 `json:"mae_dbz"`
}

// Report summarizes all verified forecasts.
type Report struct {
	Leads       []LeadStats `json:"leads"`
	Model       Scores      `json:"model"`
	Persistence Scores      `json:"persistence"`
	Arrival     ArrivalStat `json:"arrival"`
	// Shadow compares the model with and without the intensity trend on
	// the forecasts that recorded both; nil until any did.
	Shadow *Comparison `json:"shadow"`
}

// Comparison scores three predictors on exactly the same forecasts.
type Comparison struct {
	Leads       []LeadComparison `json:"leads"`
	Model       Scores           `json:"model"`
	Trend       Scores           `json:"trend"`
	Persistence Scores           `json:"persistence"`
}

// LeadComparison is one lead time of a Comparison.
type LeadComparison struct {
	LeadMin     int    `json:"lead_min"`
	Model       Scores `json:"model"`
	Trend       Scores `json:"trend"`
	Persistence Scores `json:"persistence"`
}

// Build aggregates verified rows by lead time.
func Build(rows []Row) Report {
	byLead := map[int]*LeadStats{}
	absErr := map[int]float64{}
	var r Report
	for _, row := range rows {
		ls, ok := byLead[row.LeadMin]
		if !ok {
			ls = &LeadStats{LeadMin: row.LeadMin}
			byLead[row.LeadMin] = ls
		}
		ls.Model.Add(row.PredRain, row.ObsRain)
		ls.Persistence.Add(row.PersistRain, row.ObsRain)
		r.Model.Add(row.PredRain, row.ObsRain)
		r.Persistence.Add(row.PersistRain, row.ObsRain)
		// Only score intensity where either side saw an echo; clear-sky pairs
		// would swamp the average with zeros.
		if row.PredDBZ > 0 || row.ObsDBZ > 0 {
			absErr[row.LeadMin] += math.Abs(math.Max(row.PredDBZ, 0) - math.Max(row.ObsDBZ, 0))
		}
	}
	for lead, ls := range byLead {
		ls.Model.Compute()
		ls.Persistence.Compute()
		if n := countEchoes(rows, lead); n > 0 {
			v := absErr[lead] / float64(n)
			ls.MAEdBZ = &v
		}
		r.Leads = append(r.Leads, *ls)
	}
	sort.Slice(r.Leads, func(i, j int) bool { return r.Leads[i].LeadMin < r.Leads[j].LeadMin })
	r.Model.Compute()
	r.Persistence.Compute()
	if r.Leads == nil {
		r.Leads = []LeadStats{}
	}
	r.Shadow = compare(rows)
	return r
}

// compare scores model, trend and persistence on rows that have a trend.
func compare(rows []Row) *Comparison {
	byLead := map[int]*LeadComparison{}
	c := &Comparison{}
	for _, row := range rows {
		if row.Trend == nil {
			continue
		}
		lc, ok := byLead[row.LeadMin]
		if !ok {
			lc = &LeadComparison{LeadMin: row.LeadMin}
			byLead[row.LeadMin] = lc
		}
		for _, pair := range []struct {
			lead, all *Scores
			pred      bool
		}{
			{&lc.Model, &c.Model, row.PredRain},
			{&lc.Trend, &c.Trend, row.Trend.Rain},
			{&lc.Persistence, &c.Persistence, row.PersistRain},
		} {
			pair.lead.Add(pair.pred, row.ObsRain)
			pair.all.Add(pair.pred, row.ObsRain)
		}
	}
	if len(byLead) == 0 {
		return nil
	}
	for _, lc := range byLead {
		lc.Model.Compute()
		lc.Trend.Compute()
		lc.Persistence.Compute()
		c.Leads = append(c.Leads, *lc)
	}
	sort.Slice(c.Leads, func(i, j int) bool { return c.Leads[i].LeadMin < c.Leads[j].LeadMin })
	c.Model.Compute()
	c.Trend.Compute()
	c.Persistence.Compute()
	return c
}

func countEchoes(rows []Row, lead int) int {
	n := 0
	for _, row := range rows {
		if row.LeadMin == lead && (row.PredDBZ > 0 || row.ObsDBZ > 0) {
			n++
		}
	}
	return n
}

// Issue is a forecast's headline prediction.
type Issue struct {
	IssuedAt   int64
	ArrivalMin int // -1 if no rain predicted
	RainingNow bool
}

// ArrivalStat measures how close predicted arrival times were.
type ArrivalStat struct {
	// N counts issues where rain was both predicted and observed to arrive.
	N      int      `json:"n"`
	MAEMin *float64 `json:"mae_min"`
	// BiasMin > 0 means rain arrived later than predicted.
	BiasMin *float64 `json:"bias_min"`
}

// Arrival compares predicted arrival minutes with the first later frame that
// showed rain. obs maps frame time (unix s) to whether it was raining;
// frames are stepMin apart and horizonMin bounds the search.
func Arrival(issues []Issue, obs map[int64]bool, stepMin, horizonMin int) ArrivalStat {
	var st ArrivalStat
	var sumAbs, sum float64
	for _, is := range issues {
		if is.RainingNow || is.ArrivalMin < 0 {
			continue
		}
		actual := -1
		for lead := stepMin; lead <= horizonMin; lead += stepMin {
			rain, ok := obs[is.IssuedAt+int64(lead)*60]
			if !ok {
				break // a gap makes the true arrival unknown
			}
			if rain {
				actual = lead
				break
			}
		}
		if actual < 0 {
			continue
		}
		d := float64(actual - is.ArrivalMin)
		st.N++
		sum += d
		sumAbs += math.Abs(d)
	}
	if st.N > 0 {
		mae, bias := sumAbs/float64(st.N), sum/float64(st.N)
		st.MAEMin, st.BiasMin = &mae, &bias
	}
	return st
}

// Pool combines arrival statistics from several stations, weighting each by
// its number of events.
func Pool(stats []ArrivalStat) ArrivalStat {
	var out ArrivalStat
	var sumAbs, sum float64
	for _, st := range stats {
		if st.N == 0 || st.MAEMin == nil {
			continue
		}
		out.N += st.N
		sumAbs += *st.MAEMin * float64(st.N)
		sum += *st.BiasMin * float64(st.N)
	}
	if out.N > 0 {
		mae, bias := sumAbs/float64(out.N), sum/float64(out.N)
		out.MAEMin, out.BiasMin = &mae, &bias
	}
	return out
}
