package backtest

import (
	"math"
	"testing"
)

// fssOf scores one forecast against obs on a full n×n lattice.
func fssOf(t *testing.T, n int, pred, obs []float32, win int) float64 {
	t.Helper()
	var lat lattice
	for y := range n {
		for x := range n {
			lat.put(x, y)
		}
	}
	cfg := Config{Leads: []int{10}, FSSThresholds: []float32{20}, FSSWindows: []int{win}}
	var b block
	acc := b.fssAccs(1, 1, 1, 1)
	addFSS(acc, []forecast{{dbz: [][]float32{pred}}}, [][]float32{obs}, &lat, cfg)
	s := acc[0][0][0][0].score()
	if s == nil {
		t.Fatal("no rain anywhere")
	}
	return *s
}

// square is an n×n field raining (30 dBZ) on a 3×3 square from (x0, y0).
func square(n, x0, y0 int) []float32 {
	f := make([]float32, n*n)
	for y := y0; y < y0+3; y++ {
		for x := x0; x < x0+3; x++ {
			f[y*n+x] = 30
		}
	}
	return f
}

func TestFSSRewardsNearMisses(t *testing.T) {
	const n = 12
	obs := square(n, 4, 4)
	if s := fssOf(t, n, obs, obs, 1); s != 1 {
		t.Errorf("perfect forecast FSS = %v", s)
	}
	off := square(n, 6, 4) // two points east: overlaps one column
	point, near, wide := fssOf(t, n, off, obs, 1), fssOf(t, n, off, obs, 3), fssOf(t, n, off, obs, 5)
	if !(point < near && near < wide) {
		t.Errorf("FSS should grow with the window for a near miss: %v, %v, %v", point, near, wide)
	}
	// One point: 3 hits, 6 misses, 6 false alarms.
	if want := 1 - 12.0/18.0; math.Abs(point-want) > 1e-9 {
		t.Errorf("point FSS = %v, want %v", point, want)
	}
}

func TestReportHasFSS(t *testing.T) {
	cfg := testConfig()
	rep := Run(stormFrames(14), cfg)
	for _, r := range rep.Results {
		if len(r.FSS) != len(cfg.FSSThresholds)*len(cfg.FSSWindows) {
			t.Fatalf("%s: %d FSS scores", r.Name, len(r.FSS))
		}
		if r.FSS[0].Overall == nil || len(r.FSS[0].Leads) != len(cfg.Leads) {
			t.Fatalf("%s: FSS at %v dBZ not scored", r.Name, r.FSS[0].Threshold)
		}
	}
}
