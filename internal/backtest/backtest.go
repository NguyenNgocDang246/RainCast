// Package backtest re-runs the nowcast on stored radar frames with several
// forecast methods and scores each against the frames that actually
// followed. Every collected region is scored the same way and the results
// pooled, so methods are compared on many independent rain events at once.
//
// Scores accumulate in a State, per region and six-hour block, so a
// background run only scores forecast times it has not seen before; the
// blocks are also the units the confidence intervals resample.
package backtest

import (
	"context"
	"runtime"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	"raincast/internal/model"
	"raincast/internal/radar"
	"raincast/internal/verify"
)

// Frame is a stored radar frame.
type Frame struct {
	Time   int64 // unix seconds
	Mosaic *radar.Mosaic
}

// Source supplies one region's frames on demand, so only a sliding window
// of them is ever in memory.
type Source interface {
	// Times lists the frame times that may exist, ascending.
	Times() []int64
	// Load returns the frame at t, or nil when it is missing.
	Load(t int64) *radar.Mosaic
}

// Region is one area to score: a 3×3 tile mosaic per frame.
type Region struct {
	Name    string
	Climate string
	Source  Source
	// Coverage masks pixels without radar; nil treats all as covered.
	Coverage *radar.Coverage
}

// Forecast methods: the motion methods of the model package, whose fields
// the same semi-Lagrangian nowcast extrapolates along, and the ensembles
// the backtest builds from their forecasts.
const (
	MethodTREC     = model.TREC
	MethodCOTREC   = model.COTREC
	MethodHS       = model.HS
	MethodLK       = model.LK
	MethodCellNN   = model.CellNN
	MethodCellHung = model.CellHung
	MethodHybrid   = model.Hybrid
	MethodMean     = "ensemble" // mean of the members' dBZ and probability
	MethodVote     = "vote"     // share of members forecasting rain
)

// Variant is one forecast setting to score.
type Variant struct {
	Name   string
	Method string
	Pairs  int  // frame pairs averaged for motion, newest weighted most
	Trend  bool // extrapolate intensity growth/decay
	// Members are the Names of the variants an ensemble combines; they
	// must come earlier in Config.Variants.
	Members []string
	// Weighting is the scheme of a MethodWeighted ensemble. Every scheme
	// but WeightEqual needs Members to be Config.WeightMembers.
	Weighting string
}

// Config controls the run.
type Config struct {
	Variants  []Variant
	Leads     []int // minutes, multiples of StepSec/60
	Threshold float32
	Radius    int
	TrendTau  float64
	// Points are sampled every Step pixels inside the central tile, which
	// keeps a full tile of radar around each one.
	Step    int
	StepSec int64 // seconds between frames (600)
	// EventRain is the share of the central tile that must rain for a frame
	// to count as rainy; EventFrames consecutive rainy frames make an event.
	EventRain   float64
	EventFrames int
	// Parallel is how many regions are scored at once and Workers how many
	// goroutines estimate each motion field; 0 means GOMAXPROCS. A
	// background run keeps both low so the machine stays responsive.
	Parallel, Workers int
	// Reference is the Name of the variant others are compared with.
	Reference string
	// WeightMembers are the variants whose statistics weighted ensembles
	// learn from, and Weights what was learned (from the previous run).
	WeightMembers []string
	Weights       *Weights
}

// LeadScore is a variant's skill at one lead time.
type LeadScore struct {
	LeadMin int           `json:"lead_min"`
	Scores  verify.Scores `json:"scores"`
	MAEdBZ  *float64      `json:"mae_dbz"` // over samples where either side had rain
	Brier   *float64      `json:"brier"`
}

// Interval is a 95% bootstrap confidence interval.
type Interval [2]float64

// RelBin is one bin of a reliability diagram: when the forecast said about
// P, rain followed Freq of the time, over N samples.
type RelBin struct {
	P    float64 `json:"p"`
	Freq float64 `json:"freq"`
	N    int64   `json:"n"`
}

// Result is one variant's (or the persistence baseline's) score.
type Result struct {
	Name     string        `json:"name"`
	Method   string        `json:"method"`
	Pairs    int           `json:"pairs"`
	Trend    bool          `json:"trend"`
	Baseline bool          `json:"baseline"`
	Leads    []LeadScore   `json:"leads"`
	Overall  verify.Scores `json:"overall"`
	CSICI    *Interval     `json:"csi_ci,omitempty"`
	// DeltaCSI is the overall CSI minus the reference's, with its interval
	// from the same resamples: an interval entirely above 0 means better.
	DeltaCSI   *float64  `json:"delta_csi,omitempty"`
	DeltaCSICI *Interval `json:"delta_csi_ci,omitempty"`
	// Probability scores: Brier (lower is better), its skill against
	// persistence, the Brier after calibration learned on the other half
	// of the data, and the area under the ROC curve.
	Brier       *float64 `json:"brier,omitempty"`
	BSS         *float64 `json:"bss,omitempty"`
	BrierCal    *float64 `json:"brier_cal,omitempty"`
	AUC         *float64 `json:"auc,omitempty"`
	Reliability []RelBin `json:"reliability,omitempty"`
	MsPerIssue  float64  `json:"ms_per_issue"`
}

// RegionSummary describes one region's data and each result's overall CSI,
// in the order of Report.Results.
type RegionSummary struct {
	Name    string     `json:"name"`
	Climate string     `json:"climate"`
	Frames  int        `json:"frames"`
	Skipped int        `json:"skipped"`
	Issues  int        `json:"issues"`
	Events  int        `json:"events"`
	CSI     []*float64 `json:"csi"`
}

// Group is the pooled score of the regions in one climate group.
type Group struct {
	Climate string   `json:"climate"`
	Regions int      `json:"regions"`
	Issues  int      `json:"issues"`
	Events  int      `json:"events"`
	Results []Result `json:"results"`
}

// ErrCorr is the correlation of dBZ errors between results at 30 minutes,
// over samples with rain on either side: an ensemble gains only from
// members whose errors differ.
type ErrCorr struct {
	Names []string     `json:"names"`
	R     [][]*float64 `json:"r"`
}

// Report is the outcome of a run.
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`
	Duration    string    `json:"duration"`
	Frames      int       `json:"frames"`  // frames loaded, all regions
	Skipped     int       `json:"skipped"` // frames whose tiles were missing
	Issues      int       `json:"issues"`  // forecast times scored, all regions
	NewIssues   int       `json:"new_issues"`
	Points      int       `json:"points"` // sample points per issue
	From        int64     `json:"from"`
	To          int64     `json:"to"`
	// Events counts rain events (an hour or more of rain over the central
	// tile) across regions: the independent cases the scores rest on.
	Events    int             `json:"events"`
	Blocks    int             `json:"blocks"` // six-hour blocks resampled for the intervals
	Reference string          `json:"reference"`
	Regions   []RegionSummary `json:"regions"`
	Groups    []Group         `json:"groups"`
	Results   []Result        `json:"results"` // persistence first, then variants
	ErrCorr   *ErrCorr        `json:"err_corr,omitempty"`
	// WeightMembers and Weights show what the weighted ensembles used:
	// Weights[fold][scheme][lead][member], each fold learned on the other.
	WeightMembers  []string                  `json:"weight_members,omitempty"`
	Weights        [2]map[string][][]float64 `json:"weights,omitempty"`
	WeightsLearned bool                      `json:"weights_learned"`
}

// MinEvents is the number of rain events below which the backtest cannot
// tell methods apart.
const MinEvents = 30

// Run scores in-memory frames as a single region.
func Run(frames []Frame, cfg Config) Report {
	rep, _ := RunRegions(context.Background(), []Region{{Name: "local", Source: memSource(frames)}}, cfg, nil)
	return rep
}

// RunRegions scores every region, several at once, adding the forecast
// times not yet in st (a fresh state when nil) and reporting on all of it.
func RunRegions(ctx context.Context, regions []Region, cfg Config, st *State) (Report, error) {
	cfg.Variants = slices.Clone(cfg.Variants)
	for i := range cfg.Variants {
		if cfg.Variants[i].Method == "" {
			cfg.Variants[i].Method = MethodTREC
		}
	}
	if st == nil || st.Version != cfg.version() {
		*cfgState(&st) = *NewState(cfg)
	}
	parts := make([]*State, len(regions))
	g, gctx := errgroup.WithContext(ctx)
	parallel := cfg.Parallel
	if parallel <= 0 {
		parallel = runtime.GOMAXPROCS(0)
	}
	g.SetLimit(parallel)
	for i, r := range regions {
		last := st.lastIssue(r.Name)
		g.Go(func() error {
			part, err := scoreRegion(gctx, r, cfg, last)
			parts[i] = part
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return Report{}, err
	}
	added := 0
	for _, p := range parts {
		added += p.issueCount()
		st.merge(p)
	}
	rep := st.report(cfg)
	rep.NewIssues = added
	return rep, nil
}

// cfgState makes *st point at a State, allocating one when st is nil.
func cfgState(st **State) *State {
	if *st == nil {
		*st = &State{}
	}
	return *st
}

// memSource serves frames already in memory.
type memSource []Frame

func (m memSource) Times() []int64 {
	out := make([]int64, len(m))
	for i, f := range m {
		out[i] = f.Time
	}
	sortInt64(out)
	return out
}

func (m memSource) Load(t int64) *radar.Mosaic {
	for _, f := range m {
		if f.Time == t {
			return f.Mosaic
		}
	}
	return nil
}
