package nowcast

import (
	"math"
	"testing"

	"raincast/internal/motion"
	"raincast/internal/radar"
)

func uniformField(v motion.Vector) *motion.Field {
	f := &motion.Field{BlockSize: 32, BW: 4, BH: 4, V: make([]motion.Vector, 16), Valid: make([]bool, 16), Global: v, NValid: 16}
	for i := range f.V {
		f.V[i] = v
		f.Valid[i] = true
	}
	return f
}

func disc(g *radar.Grid, cx, cy, r int, dbz float32) {
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r {
				g.Set(x, y, dbz)
			}
		}
	}
}

func TestArrival(t *testing.T) {
	g := radar.NewGrid(128, 128)
	// Cell edge 20 px west of the target, moving east at 4 px / 10 min.
	disc(g, 64-20-5, 64, 5, 40)
	f := uniformField(motion.Vector{DX: 0.4})
	r := Forecast(g, f, 64, 64, Options{Horizon: 60, Threshold: 20, Radius: 0, KmPerPx: 1.2})
	if r.RainingNow {
		t.Fatal("should not be raining now")
	}
	if r.ArrivalMin < 48 || r.ArrivalMin > 52 {
		t.Fatalf("arrival = %d, want ~50", r.ArrivalMin)
	}
	if math.Abs(r.DirectionDeg-90) > 1e-6 {
		t.Fatalf("direction = %v, want 90 (east)", r.DirectionDeg)
	}
	if math.Abs(r.SpeedKmh-0.4*1.2*60) > 1e-6 {
		t.Fatalf("speed = %v", r.SpeedKmh)
	}
	if len(r.Series) != 61 {
		t.Fatalf("series len = %d", len(r.Series))
	}
}

func TestRainingNowAndMovingAway(t *testing.T) {
	g := radar.NewGrid(128, 128)
	disc(g, 64, 64, 3, 35)
	r := Forecast(g, uniformField(motion.Vector{DX: 0.5}), 64, 64, Options{Horizon: 60, Threshold: 20, KmPerPx: 1.2})
	if !r.RainingNow || r.ArrivalMin != 0 {
		t.Fatalf("raining=%v arrival=%d", r.RainingNow, r.ArrivalMin)
	}
	if r.At(60) >= 20 {
		t.Fatalf("cell should have passed by minute 60, got %v", r.At(60))
	}
}

// A core smaller than the median's disc still rains on the point under it.
func TestRainingUnderSmallCore(t *testing.T) {
	g := radar.NewGrid(128, 128)
	disc(g, 64, 64, 1, 40)
	opt := Options{Horizon: 10, Threshold: 20, Radius: 2, KmPerPx: 1.2}
	if r := Forecast(g, nil, 64, 64, opt); r.RainingNow {
		t.Fatal("the median alone should miss the core")
	}
	opt.Strong = 30
	if r := Forecast(g, nil, 64, 64, opt); !r.RainingNow || r.At(0) != 40 {
		t.Fatalf("raining=%v at=%v, want the core's 40 dBZ", r.RainingNow, r.At(0))
	}
}

func TestNoRain(t *testing.T) {
	r := Forecast(radar.NewGrid(64, 64), nil, 32, 32, Options{Horizon: 60, Threshold: 20})
	if r.ArrivalMin != -1 || r.RainingNow || r.MotionReliable {
		t.Fatalf("%+v", r)
	}
}

func TestHeavyArrivalWhileLightRain(t *testing.T) {
	g := radar.NewGrid(128, 128)
	disc(g, 64, 64, 40, 25)   // light rain everywhere around the target
	disc(g, 64-20, 64, 4, 45) // heavy core 16 px west, moving east 0.4 px/min
	r := Forecast(g, uniformField(motion.Vector{DX: 0.4}), 64, 64, Options{Horizon: 60, Threshold: 20, Heavy: 40, KmPerPx: 1.2})
	if !r.RainingNow || r.HeavyNow {
		t.Fatalf("raining=%v heavy=%v", r.RainingNow, r.HeavyNow)
	}
	if r.HeavyArrivalMin < 38 || r.HeavyArrivalMin > 42 {
		t.Fatalf("heavy arrival = %d, want ~40", r.HeavyArrivalMin)
	}
}

func TestTrendGrowsLightRainIntoHeavy(t *testing.T) {
	g := radar.NewGrid(128, 128)
	disc(g, 64, 64, 20, 30) // moderate rain over the target, not moving
	still := uniformField(motion.Vector{})
	growing := &motion.Trend{BlockSize: 32, BW: 4, BH: 4, R: make([]float64, 16)}
	for i := range growing.R {
		growing.R[i] = 0.8 // dBZ/min
	}
	base := Forecast(g, still, 64, 64, Options{Horizon: 60, Threshold: 20, Heavy: 40})
	if base.HeavyArrivalMin != -1 {
		t.Fatalf("without trend heavy = %d", base.HeavyArrivalMin)
	}
	r := Forecast(g, still, 64, 64, Options{Horizon: 60, Threshold: 20, Heavy: 40, Trend: growing, TrendTau: 20})
	// 30 + 0.8·20·(1−e^(−m/20)) reaches 40 near m = 19.
	if r.HeavyArrivalMin < 17 || r.HeavyArrivalMin > 21 {
		t.Fatalf("heavy arrival = %d, want ~19", r.HeavyArrivalMin)
	}
	if r.At(60) > 30+0.8*20+0.01 {
		t.Fatalf("trend did not saturate: %v", r.At(60))
	}
}

func TestProbability(t *testing.T) {
	g := radar.NewGrid(100, 100)
	for y := range 100 {
		for x := 50; x < 100; x++ {
			g.Set(x, y, 35) // rain on the east half
		}
	}
	opt := Options{Horizon: 30, Threshold: 20, Radius: 1, ProbRadius: func(int) int { return 4 }}
	r := Forecast(g, nil, 50, 50, opt)
	if p := r.ProbAt(0); p < 0.4 || p > 0.65 {
		t.Errorf("on the edge: prob %.2f, want about half", p)
	}
	if p := Forecast(g, nil, 80, 50, opt).ProbAt(10); p != 1 {
		t.Errorf("inside the rain: prob %.2f, want 1", p)
	}
	if p := Forecast(g, nil, 10, 50, opt).ProbAt(10); p != 0 {
		t.Errorf("far from rain: prob %.2f, want 0", p)
	}
	if p := Forecast(g, nil, 50, 50, Options{Horizon: 5, Threshold: 20, Radius: 1}).ProbAt(0); p != 0 {
		t.Errorf("without ProbRadius prob = %.2f, want 0", p)
	}
	if DefaultProbRadius(0) != 2 || DefaultProbRadius(60) != 10 {
		t.Error("DefaultProbRadius should go from 2 to 10 px over an hour")
	}
}

// rotation is solid-body rotation about (cx, cy): ω rad/min, sampled per
// 8-px block so bilinear interpolation stays close to exact.
func rotation(n int, cx, cy, omega float64) *motion.Field {
	bs, bw := 8, n/8
	v := make([]motion.Vector, bw*bw)
	valid := make([]bool, bw*bw)
	for by := range bw {
		for bx := range bw {
			x, y := (float64(bx)+0.5)*float64(bs)-cx, (float64(by)+0.5)*float64(bs)-cy
			v[by*bw+bx] = motion.Vector{DX: -omega * y, DY: omega * x}
			valid[by*bw+bx] = true
		}
	}
	return motion.FromBlocks(bs, bw, bw, v, valid)
}

// In a rotating field the upstream point stays on the circle; Euler steps
// spiral outward, midpoint steps do not.
func TestMidpointFollowsRotation(t *testing.T) {
	const n, c, rad = 256, 128.0, 60.0
	f := rotation(n, c, c, 2*math.Pi/120) // a full turn in two hours
	g := radar.NewGrid(n, n)
	// Rain only on the circle a quarter turn (30 min) upstream of the target.
	disc(g, int(c), int(c-rad), 1, 40)
	r := Forecast(g, f, c+rad, c, Options{Horizon: 30, Threshold: 20})
	if r.At(30) < 20 {
		t.Fatalf("rain a quarter turn upstream did not arrive: %v dBZ at 30'", r.At(30))
	}
}

// 40 dBZ (≈ 11.5 mm/h) for half an hour is ≈ 5.8 mm.
func TestAccumulation(t *testing.T) {
	g := radar.NewGrid(128, 128)
	disc(g, 64, 64, 40, 40)
	r := Forecast(g, uniformField(motion.Vector{}), 64, 64, Options{Horizon: 30, Threshold: 20})
	if math.Abs(r.AccumMM-radar.RainRate(40)/2) > 1e-9 || math.Abs(r.AccumMM-5.75) > 0.1 {
		t.Fatalf("accum = %.2f mm, want ≈ 5.75", r.AccumMM)
	}
	var sum float64
	for _, pt := range r.Series[1:] {
		sum += float64(pt.MM)
	}
	if math.Abs(sum-r.AccumMM) > 1e-3 {
		t.Fatalf("points' mm total %.4f, accum %.4f", sum, r.AccumMM)
	}
	if d := Forecast(radar.NewGrid(64, 64), nil, 32, 32, Options{Horizon: 60, Threshold: 20}); d.AccumMM != 0 {
		t.Fatalf("dry accum = %v", d.AccumMM)
	}
}
