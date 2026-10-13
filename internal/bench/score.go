package bench

import (
	"math"
	"slices"
	"time"

	"raincast/internal/radar"
	"raincast/internal/store"
	"raincast/internal/verify"
)

// ThresholdDBZ is the echo counted as rain, as in the app; for providers
// that give mm/h, ThresholdMMH.
const ThresholdDBZ = 20

// ThresholdMMH is ThresholdDBZ as a rain rate.
var ThresholdMMH = radar.RainRate(ThresholdDBZ)

// Forecasters are scored in this order; truths are listed in TruthOrder.
var (
	ForecasterOrder = []string{NameRainCast, NameTomorrow, NameRainbow}
	TruthOrder      = []string{NameMETAR, NameRainViewer, NameTomorrow, NameRainbow}
)

// Report is the benchmark over a span of stored forecasts.
type Report struct {
	GeneratedAt  time.Time        `json:"generated_at"`
	From         int64            `json:"from"` // first valid time scored, unix s (0 when none)
	To           int64            `json:"to"`
	Stations     []string         `json:"stations"`
	ThresholdMMH float64          `json:"threshold_mm_h"`
	Leads        []int            `json:"leads"` // minutes
	Providers    []ProviderStatus `json:"providers"`
	Tables       []Table          `json:"tables"`
}

// ProviderStatus is what was recorded from a provider.
type ProviderStatus struct {
	Name       string         `json:"name"`
	Issues     int            `json:"issues"`      // calls stored (station × issue time)
	LastIssued int64          `json:"last_issued"` // unix s
	Kinds      map[string]int `json:"kinds"`       // samples by kind
}

// Table scores every forecaster against one source's later "now".
type Table struct {
	Truth string `json:"truth"`
	// ThresholdMMH is the forecast rain rate counted as rain: weatherindex.ai's
	// 0.5 mm/h against METAR, the app's 20 dBZ against the others.
	ThresholdMMH float64 `json:"threshold_mm_h"`
	// Times is how many (station, time) observations the source has.
	Times int   `json:"times"`
	Rows  []Row `json:"rows"`
}

// Row is a forecaster's scores, lead by lead. The persistence row
// forecasts the truth source's own value at issue time to stay.
type Row struct {
	Forecaster  string        `json:"forecaster"`
	Persistence bool          `json:"persistence,omitempty"`
	Leads       []Cell        `json:"leads"`
	Overall     verify.Scores `json:"overall"`
	MAE         *float64      `json:"mae_mm_h"`
}

// Cell is a forecaster's scores at one lead.
type Cell struct {
	Lead   int           `json:"lead_min"`
	Scores verify.Scores `json:"scores"`
	MAE    *float64      `json:"mae_mm_h"` // mean absolute error, mm/h
}

type issueKey struct {
	station       string
	issued, valid int64
}

type timeKey struct {
	station string
	t       int64
}

// truthVal is a source's observation: rain or not, and the rate when the
// source gives one (METAR does not).
type truthVal struct {
	rain   bool
	mmh    float64
	hasMMH bool
}

type acc struct {
	s      verify.Scores
	absErr float64
	nErr   int
}

// add records a forecast of f mm/h (rain when pred) against o.
func (a *acc) add(pred bool, f float64, o truthVal) {
	a.s.Add(pred, o.rain)
	if o.hasMMH {
		a.absErr += math.Abs(f - o.mmh)
		a.nErr++
	}
}

func (a *acc) mae() *float64 {
	if a.nErr == 0 {
		return nil
	}
	v := a.absErr / float64(a.nErr)
	return &v
}

// metarTruth gives each scored time (a multiple of step) the nearest
// report within MetarMatch, as weatherindex.ai matches forecasts to METAR.
func metarTruth(reps []store.BenchMetar, step int64) map[timeKey]truthVal {
	type near struct {
		dist, obs int64
		rain      bool
	}
	best := map[timeKey]near{}
	tol := int64(MetarMatch / time.Second)
	for _, r := range reps {
		first := (r.Obs - tol + step - 1) / step * step
		for t := first; t <= r.Obs+tol; t += step {
			d := r.Obs - t
			if d < 0 {
				d = -d
			}
			k := timeKey{r.Station, t}
			// On a tie the earlier report wins: the weather up to t.
			if b, ok := best[k]; !ok || d < b.dist || (d == b.dist && r.Obs < b.obs) {
				best[k] = near{d, r.Obs, r.Rain}
			}
		}
	}
	out := make(map[timeKey]truthVal, len(best))
	for k, b := range best {
		out[k] = truthVal{rain: b.rain}
	}
	return out
}

// Score builds the report from stored forecasts, radar observations and
// METAR reports (whose station is the ICAO code a forecast station is named by).
// Each table only counts (station, time, lead) samples that the truth
// source, its persistence and every forecaster with data all have, so the
// rows of a table compare like with like.
func Score(fc []store.BenchForecast, obs []store.BenchObs, metars []store.BenchMetar, now time.Time) Report {
	step := int64(Step / time.Second)
	rep := Report{GeneratedAt: now.UTC(), ThresholdMMH: ThresholdMMH, Stations: []string{}, Providers: []ProviderStatus{}, Tables: []Table{}}
	for l := step; l <= int64(Horizon/time.Second); l += step {
		rep.Leads = append(rep.Leads, int(l/60))
	}

	vals := map[string]map[issueKey]float64{}
	truth := map[string]map[timeKey]float64{} // mm/h
	status := map[string]*ProviderStatus{}
	issues := map[string]map[timeKey]bool{}
	stations := map[string]bool{}
	setTruth := func(src string, k timeKey, v float64) {
		if truth[src] == nil {
			truth[src] = map[timeKey]float64{}
		}
		truth[src][k] = v
	}
	for _, f := range fc {
		stations[f.Station] = true
		if vals[f.Provider] == nil {
			vals[f.Provider] = map[issueKey]float64{}
			issues[f.Provider] = map[timeKey]bool{}
			status[f.Provider] = &ProviderStatus{Name: f.Provider, Kinds: map[string]int{}}
		}
		vals[f.Provider][issueKey{f.Station, f.Issued, f.Valid}] = f.MMH
		st := status[f.Provider]
		st.Kinds[f.Kind]++
		st.LastIssued = max(st.LastIssued, f.Issued)
		issues[f.Provider][timeKey{f.Station, f.Issued}] = true
		// A provider's own "now" is its observation. RainCast's is the
		// radar frame, recorded apart.
		if f.Issued == f.Valid && f.Provider != NameRainCast {
			setTruth(f.Provider, timeKey{f.Station, f.Valid}, f.MMH)
		}
	}
	for _, o := range obs {
		stations[o.Station] = true
		setTruth(NameRainViewer, timeKey{o.Station, o.Frame}, radar.RainRate(float32(o.DBZ)))
	}
	metar := metarTruth(metars, step)
	for name, st := range status {
		st.Issues = len(issues[name])
	}
	for _, name := range ForecasterOrder {
		if st, ok := status[name]; ok {
			rep.Providers = append(rep.Providers, *st)
		}
	}
	for s := range stations {
		rep.Stations = append(rep.Stations, s)
	}
	slices.Sort(rep.Stations)

	var forecasters []string
	for _, name := range ForecasterOrder {
		if len(vals[name]) > 0 {
			forecasters = append(forecasters, name)
		}
	}

	for _, src := range TruthOrder {
		thr := ThresholdMMH
		var tr map[timeKey]truthVal
		if src == NameMETAR {
			thr, tr = MetarThresholdMMH, metar
		} else {
			tr = make(map[timeKey]truthVal, len(truth[src]))
			for k, v := range truth[src] {
				tr[k] = truthVal{rain: v >= thr, mmh: v, hasMMH: true}
			}
		}
		if len(tr) == 0 {
			continue
		}
		nLead := len(rep.Leads)
		// Row 0 is persistence, then the forecasters.
		accs := make([][]acc, len(forecasters)+1)
		for i := range accs {
			accs[i] = make([]acc, nLead)
		}
		fv := make([]float64, len(forecasters))
		for k, o := range tr {
			for li := range nLead {
				issued := k.t - int64(rep.Leads[li])*60
				p, ok := tr[timeKey{k.station, issued}]
				if !ok {
					continue
				}
				all := true
				for fi, name := range forecasters {
					if fv[fi], ok = vals[name][issueKey{k.station, issued, k.t}]; !ok {
						all = false
						break
					}
				}
				if !all {
					continue
				}
				accs[0][li].add(p.rain, p.mmh, o)
				for fi := range forecasters {
					accs[fi+1][li].add(fv[fi] >= thr, fv[fi], o)
				}
				if rep.From == 0 || k.t < rep.From {
					rep.From = k.t
				}
				rep.To = max(rep.To, k.t)
			}
		}
		t := Table{Truth: src, ThresholdMMH: thr, Times: len(tr)}
		for i, a := range accs {
			row := Row{Forecaster: src, Persistence: i == 0}
			if i > 0 {
				row.Forecaster = forecasters[i-1]
			}
			var total acc
			for li := range a {
				a[li].s.Compute()
				row.Leads = append(row.Leads, Cell{Lead: rep.Leads[li], Scores: a[li].s, MAE: a[li].mae()})
				total.s.Merge(a[li].s)
				total.absErr += a[li].absErr
				total.nErr += a[li].nErr
			}
			total.s.Compute()
			row.Overall, row.MAE = total.s, total.mae()
			t.Rows = append(t.Rows, row)
		}
		rep.Tables = append(rep.Tables, t)
	}
	return rep
}
