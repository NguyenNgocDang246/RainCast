package backtest

import (
	"context"
	"math"
	"testing"

	"raincast/internal/radar"
)

// storms draws two cells on a 3×3-tile-like mosaic: one moving east, one
// south-east, both steady.
func storms(k int) *radar.Mosaic {
	g := radar.NewGrid(288, 288)
	for _, c := range [][4]float64{{60 + 5*float64(k), 130, 22, 50}, {120 + 3*float64(k), 90 + 3*float64(k), 16, 45}} {
		for y := 0; y < 288; y++ {
			for x := 0; x < 288; x++ {
				if d := math.Hypot(float64(x)-c[0], float64(y)-c[1]); d < c[2] {
					g.Set(x, y, max(g.At(x, y), float32(c[3]-20*d/c[2])))
				}
			}
		}
	}
	return &radar.Mosaic{Grid: g}
}

func stormFrames(n int) []Frame {
	var frames []Frame
	for i := range n {
		frames = append(frames, Frame{Time: int64(600 * (i + 1)), Mosaic: storms(i)})
	}
	return frames
}

func testConfig() Config {
	cfg := DefaultConfig(6)
	cfg.Leads = []int{10, 20, 30}
	return cfg
}

func TestEveryMethodBeatsPersistenceOnSteadyStorms(t *testing.T) {
	rep := Run(stormFrames(14), testConfig())
	if rep.Issues == 0 {
		t.Fatal("nothing scored")
	}
	persist := *rep.Results[0].Overall.CSI
	for _, r := range rep.Results[1:] {
		if r.Overall.CSI == nil || *r.Overall.CSI <= persist {
			t.Errorf("%s: CSI %v does not beat persistence %.3f", r.Name, r.Overall.CSI, persist)
		}
		if r.Brier == nil || r.AUC == nil || r.BrierCal == nil {
			t.Errorf("%s: probability scores missing", r.Name)
		}
	}
	if rep.ErrCorr == nil || len(rep.ErrCorr.Names) != len(rep.Results) {
		t.Fatal("error correlations missing")
	}
	if r := rep.ErrCorr.R[1][1]; r == nil || math.Abs(*r-1) > 1e-9 {
		t.Errorf("self-correlation = %v, want 1", r)
	}
}

// Scoring in two runs, the second only adding the new forecast times, must
// give exactly the totals of one run over everything.
func TestIncrementalEqualsFresh(t *testing.T) {
	cfg := testConfig()
	cfg.Variants = cfg.Variants[:2] // TREC and HS: fast and enough here
	all := stormFrames(16)
	fresh := Run(all, cfg)

	st := NewState(cfg)
	reg := func(frames []Frame) []Region { return []Region{{Name: "local", Source: memSource(frames)}} }
	if _, err := RunRegions(context.Background(), reg(all[:13]), cfg, st); err != nil {
		t.Fatal(err)
	}
	inc, err := RunRegions(context.Background(), reg(all), cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	if inc.Issues != fresh.Issues || inc.NewIssues == 0 || inc.NewIssues == inc.Issues {
		t.Fatalf("issues: incremental %d (%d new), fresh %d", inc.Issues, inc.NewIssues, fresh.Issues)
	}
	for i := range fresh.Results {
		a, b := fresh.Results[i].Overall, inc.Results[i].Overall
		if a.Hits != b.Hits || a.Misses != b.Misses || a.FalseAlarms != b.FalseAlarms || a.CorrectNeg != b.CorrectNeg {
			t.Errorf("%s: fresh %+v, incremental %+v", fresh.Results[i].Name, a, b)
		}
	}
}

func TestCombine(t *testing.T) {
	a, b := newForecast(1, 2), newForecast(1, 2)
	a.dbz[0][0], b.dbz[0][0] = 30, 10
	a.prob[0][0], b.prob[0][0] = 0.8, 0.2
	fc := []forecast{{}, a, b}
	byName := map[string]int{"a": 1, "b": 2}
	mean := combine(fc, Variant{Method: MethodMean, Members: []string{"a", "b"}}, byName, 1, 2)
	if mean.dbz[0][0] != 20 || math.Abs(float64(mean.prob[0][0])-0.5) > 1e-6 {
		t.Errorf("mean = %v dBZ, %v", mean.dbz[0][0], mean.prob[0][0])
	}
}

func TestIsotonicAndAUC(t *testing.T) {
	var h hist
	// Forecasts in bin 2 verify 80% of the time, in bin 15 only 30%:
	// overconfident at the top, and isotonic pooling evens them out.
	h.Pos[2], h.Neg[2] = 8, 2
	h.Pos[15], h.Neg[15] = 3, 7
	c := isotonic(h)
	for b := 1; b < nBins; b++ {
		if c[b] < c[b-1] {
			t.Fatalf("calibration not monotone: %v", c)
		}
	}
	if math.Abs(c[2]-0.55) > 1e-9 || math.Abs(c[15]-0.55) > 1e-9 {
		t.Errorf("pooled value %v / %v, want 0.55", c[2], c[15])
	}
	var perfect hist
	perfect.Pos[19], perfect.Neg[0] = 10, 10
	if a := auc(perfect); a == nil || *a != 1 {
		t.Errorf("perfect AUC = %v", a)
	}
	var useless hist
	useless.Pos[10], useless.Neg[10] = 10, 10
	if a := auc(useless); a == nil || math.Abs(*a-0.5) > 1e-9 {
		t.Errorf("no-skill AUC = %v", a)
	}
}

func TestStateSaveLoad(t *testing.T) {
	cfg := testConfig()
	cfg.Variants = cfg.Variants[:2]
	st := NewState(cfg)
	if _, err := RunRegions(context.Background(), []Region{{Name: "r", Source: memSource(stormFrames(12))}}, cfg, st); err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/s.gob"
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	got := LoadState(path)
	if got == nil || got.Version != st.Version || got.issueCount() != st.issueCount() || got.issueCount() == 0 {
		t.Fatalf("reloaded state differs")
	}
}

func TestNoIntervalsFromTooFewBlocks(t *testing.T) {
	rep := Run(stormFrames(14), testConfig())
	if rep.Blocks >= minBlocks {
		t.Skip("enough blocks")
	}
	for _, r := range rep.Results {
		if r.CSICI != nil || r.DeltaCSICI != nil {
			t.Fatalf("%s has intervals from %d blocks", r.Name, rep.Blocks)
		}
	}
}

func TestBootstrapIntervals(t *testing.T) {
	cfg := testConfig()
	cfg.Variants = cfg.Variants[:3]
	st := NewState(cfg)
	// Twelve blocks: the reference always hits; variant 2 misses a third.
	for i := range 12 {
		b := st.block("r", "tropical", int64(i)*blockSec, 4, len(cfg.Classes), len(cfg.Leads))
		b.Issues = 1
		for v := range 4 {
			b.Acc[v][0] = leadAcc{H: 10, M: 0, F: 0}
		}
		b.Acc[2][0] = leadAcc{H: 6, M: 3}
	}
	rep := st.report(cfg)
	ref := rep.Results[1] // TREC 4 cặp
	if ref.Name != cfg.Reference {
		t.Fatalf("row 1 is %s", ref.Name)
	}
	r := rep.Results[2]
	if r.DeltaCSICI == nil || r.DeltaCSICI[1] >= 0 {
		t.Fatalf("a variant always worse should have an interval below 0: %+v", r.DeltaCSICI)
	}
}

func TestClassScores(t *testing.T) {
	if !(Class{Lo: 20, Hi: 30}).in(25) || (Class{Lo: 20, Hi: 30}).in(30) || !(Class{Lo: 50}).in(65) {
		t.Fatal("class bounds")
	}
	cfg := testConfig()
	cfg.Variants = cfg.Variants[:3]
	cfg.Classes = []Class{{Name: "light", Lo: 20, Hi: 30}, {Name: "heavy", Lo: 40}}
	st := NewState(cfg)
	// The reference catches light rain but calls heavy rain light; variant
	// 2 gets both.
	for i := range 12 {
		b := st.block("r", "tropical", int64(i)*blockSec, 4, len(cfg.Classes), len(cfg.Leads))
		b.Issues = 1
		for v := range 4 {
			b.Cls[v][0][0] = classAcc{H: 10}
			b.Cls[v][1][0] = classAcc{M: 5}
		}
		b.Cls[2][1][0] = classAcc{H: 5}
	}
	rep := st.report(cfg)
	heavy := func(r Result) ClassScore { return r.Classes[1] }
	if c := heavy(rep.Results[1]); *c.Overall.CSI != 0 || c.Observed != 60 {
		t.Fatalf("reference on heavy rain: %+v", c.Overall)
	}
	c := heavy(rep.Results[2])
	if *c.Overall.CSI != 1 || c.DeltaCSI == nil || *c.DeltaCSI != 1 || c.DeltaCSICI[0] <= 0 {
		t.Fatalf("variant 2 on heavy rain: CSI %v, delta %v %v", c.Overall.CSI, c.DeltaCSI, c.DeltaCSICI)
	}
	if l := rep.Results[2].Classes[0]; *l.Overall.CSI != 1 || l.Class != "light" {
		t.Fatalf("light rain: %+v", l)
	}
}
