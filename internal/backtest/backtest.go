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
	"sync/atomic"
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
	// Lat, Lon are the center and TileX, TileY the center radar tile, for
	// the ML features (local time, satellite, NWP); zero when unknown.
	Lat, Lon     float64
	TileX, TileY int
	Source       Source
	// Coverage masks pixels without radar; nil treats all as covered.
	Coverage *radar.Coverage
}

// Forecast methods: the motion methods of the model package, whose fields
// the same semi-Lagrangian nowcast extrapolates along, and the ensembles
// the backtest builds from their forecasts.
const (
	MethodTREC = model.TREC
	MethodHS   = model.HS
	MethodLK   = model.LK
	MethodMean = "ensemble" // mean of the members' dBZ and probability
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
	// MLSet is the feature set of a MethodML variant (ml.Sets).
	MLSet string
}

// Config controls the run.
type Config struct {
	Variants  []Variant
	Leads     []int // minutes, multiples of StepSec/60
	Threshold float32
	// Classes are the kinds of rain also scored on their own.
	Classes []Class
	// FSSThresholds (dBZ) and FSSWindows (odd, in sample points across)
	// are where the fractions skill score is measured.
	FSSThresholds []float32
	FSSWindows    []int
	Radius        int
	// Strong is the dBZ at which a sample takes the echo at its point rather
	// than the median within Radius (radar.Grid.PointEcho); 0 never does.
	Strong   float32
	TrendTau float64
	// Points are sampled every Step pixels inside the central tile, which
	// keeps a full tile of radar around each one.
	Step    int
	StepSec int64 // seconds between frames (600)
	// Since (unix seconds) skips earlier forecast times, which still serve
	// as history: score only what models trained before it never saw.
	Since int64
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
	// Compare are pairs of variant Names, {base, other}, whose difference
	// in CSI over the leads up to CompareLead is reported with an interval
	// (Comparison), overall and per group.
	Compare [][2]string
	// ML feeds the ML variants and the training dump; nil disables both.
	ML *MLSetup
	// Progress, when set, is called every ProgressEvery while regions are
	// scored, and once at the end, with the forecast times looked at so
	// far and in all (both count times without enough frames, which are
	// quick), and the time since scoring started.
	Progress func(done, total int64, elapsed time.Duration)

	done *atomic.Int64 // forecast times looked at, shared by the regions
}

// ProgressEvery is how often Config.Progress is called.
const ProgressEvery = 30 * time.Second

// LeadScore is a variant's skill at one lead time.
type LeadScore struct {
	LeadMin int           `json:"lead_min"`
	Scores  verify.Scores `json:"scores"`
	MAEdBZ  *float64      `json:"mae_dbz"` // over samples where either side had rain
	Brier   *float64      `json:"brier"`
	// AccumMAE is the mean |pred − obs| rain accumulated up to this lead,
	// in mm, both sides from radar through radar.RainRate.
	AccumMAE *float64 `json:"accum_mae_mm,omitempty"`
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
	// CSIMean60 and CSIMean90 are meanCSI up to 60 and 90 minutes.
	CSIMean60 *float64  `json:"csi_mean_60,omitempty"`
	CSIMean90 *float64  `json:"csi_mean_90,omitempty"`
	CSICI     *Interval `json:"csi_ci,omitempty"`
	// DeltaCSI is the overall CSI minus the reference's, with its interval
	// from the same resamples: an interval entirely above 0 means better.
	DeltaCSI   *float64  `json:"delta_csi,omitempty"`
	DeltaCSICI *Interval `json:"delta_csi_ci,omitempty"`
	// Probability scores: Brier (lower is better), its skill against
	// persistence, the Brier after calibration learned on the other half
	// of the data, and the area under the ROC curve.
	Brier    *float64 `json:"brier,omitempty"`
	BSS      *float64 `json:"bss,omitempty"`
	BrierCal *float64 `json:"brier_cal,omitempty"`
	AUC      *float64 `json:"auc,omitempty"`
	// AccumMAE is LeadScore.AccumMAE at the last lead: the error in the
	// hour's rain total.
	AccumMAE    *float64 `json:"accum_mae_mm,omitempty"`
	Reliability []RelBin `json:"reliability,omitempty"`
	// Classes are the scores on each kind of rain (Config.Classes).
	Classes []ClassScore `json:"classes,omitempty"`
	// FSS are the fractions skill scores, threshold by threshold and window
	// by window (Config.FSSThresholds × Config.FSSWindows).
	FSS        []FSSScore `json:"fss,omitempty"`
	MsPerIssue float64    `json:"ms_per_issue"`
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

// Group is the pooled score of a set of regions: a climate ("tropical",
// "subtropical", "midlat") or a satellite split ("sat": seen by Himawari,
// "nosat", "tropical_sat").
type Group struct {
	Climate     string       `json:"climate"`
	Regions     int          `json:"regions"`
	Issues      int          `json:"issues"`
	Events      int          `json:"events"`
	Results     []Result     `json:"results"`
	Comparisons []Comparison `json:"comparisons,omitempty"`
}

// CompareLead is the last lead (minutes) comparisons pool: what the app
// shows.
const CompareLead = 60

// Comparison is Other's CSI minus Base's, pooled over the leads up to
// MaxLead, with its 95% interval from block resampling: entirely above 0
// means Other is better.
type Comparison struct {
	Base    string    `json:"base"`
	Other   string    `json:"other"`
	MaxLead int       `json:"max_lead"`
	Delta   *float64  `json:"delta"`
	CI      *Interval `json:"ci,omitempty"`
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
	// Comparisons are Config.Compare over every region.
	Comparisons []Comparison `json:"comparisons,omitempty"`
	ErrCorr     *ErrCorr     `json:"err_corr,omitempty"`
}

// MinEvents is the number of rain events below which the backtest cannot
// tell methods apart.
const MinEvents = 30

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
	if cfg.Progress != nil {
		var total int64
		for _, r := range regions {
			last := st.lastIssue(r.Name)
			for _, t := range r.Source.Times() {
				if t > last {
					total++
				}
			}
		}
		cfg.done = &atomic.Int64{}
		start := time.Now()
		stop := make(chan struct{})
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			tick := time.NewTicker(ProgressEvery)
			defer tick.Stop()
			for {
				select {
				case <-stop:
					cfg.Progress(cfg.done.Load(), total, time.Since(start))
					return
				case <-tick.C:
					cfg.Progress(cfg.done.Load(), total, time.Since(start))
				}
			}
		}()
		defer func() {
			close(stop)
			<-finished
		}()
	}
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
