package model

import (
	"math"
	"testing"

	"raincast/internal/motion"
	"raincast/internal/nowcast"
	"raincast/internal/radar"
)

// cellsAt draws a few rain cells shifted dx pixels east.
func cellsAt(dx int) *radar.Grid {
	g := radar.NewGrid(256, 256)
	for _, c := range [][3]int{{70, 90, 16}, {150, 70, 12}, {110, 170, 20}} {
		for y := c[1] - c[2]; y <= c[1]+c[2]; y++ {
			for x := c[0] - c[2]; x <= c[0]+c[2]; x++ {
				d := math.Hypot(float64(x-c[0]), float64(y-c[1])) / float64(c[2])
				if d <= 1 {
					g.Set(x+dx, y, float32(50-25*d))
				}
			}
		}
	}
	return g
}

// builder serves frames 10 minutes apart at the given eastward positions;
// a negative position is a missing frame.
func builder(positions []int) (*Builder, int64) {
	grids := map[int64]*radar.Grid{}
	for i, x := range positions {
		if x >= 0 {
			grids[int64(600*(i+1))] = cellsAt(x)
		}
	}
	b := &Builder{
		Grid: func(t int64) *radar.Grid { return grids[t] },
		Prev: func(t int64) (int64, bool) {
			p := t - 600
			return p, grids[p] != nil
		},
		Cache: NewCache(),
	}
	return b, int64(600 * len(positions))
}

func globalDX(t *testing.T, positions []int, pairs int) float64 {
	t.Helper()
	b, last := builder(positions)
	f, used := b.Field(TREC, last, pairs)
	if f == nil || used != min(pairs, len(positions)-1)+1 {
		t.Fatalf("field=%v used=%d", f, used)
	}
	return f.Global.DX * 10
}

func TestMotionSteadyDrift(t *testing.T) {
	if dx := globalDX(t, []int{0, 4, 8, 12, 16}, 4); math.Abs(dx-4) > 1 {
		t.Fatalf("steady drift = %.2f px/10min, want 4", dx)
	}
}

// Rain flickering between two spots must not be extrapolated as a drift.
func TestMotionOscillationCancels(t *testing.T) {
	if dx := globalDX(t, []int{0, 6, 0, 6, 0}, 1); math.Abs(dx+6) > 1 {
		t.Fatalf("single pair = %.2f, want -6", dx)
	}
	if dx := globalDX(t, []int{0, 6, 0, 6, 0}, 4); math.Abs(dx) > 1.5 {
		t.Fatalf("4 pairs = %.2f px/10min, want ~0", dx)
	}
}

// A missing frame ends the history: motion uses only the frames after it.
func TestFieldStopsAtGap(t *testing.T) {
	b, last := builder([]int{0, 4, -1, 12, 16})
	f, used := b.Field(TREC, last, 4)
	if f == nil || used != 2 {
		t.Fatalf("used %d frames, want 2 (after the gap)", used)
	}
	if f, _ := b.Field(TREC, 600, 4); f != nil {
		t.Fatal("first frame has no earlier one, want no field")
	}
}

func TestEveryMethodBuildsAField(t *testing.T) {
	b, last := builder([]int{0, 4, 8, 12, 16})
	for _, m := range Methods {
		f, used := b.Field(m, last, 4)
		if f == nil || used != 5 {
			t.Errorf("%s: field %v, used %d", m, f, used)
			continue
		}
		if v := f.At(110, 170); math.Abs(v.DX*10-4) > 1.5 {
			t.Errorf("%s: motion at a cell %.2f px/10min, want ≈ 4", m, v.DX*10)
		}
	}
}

// An ensemble of one method is that method.
func TestEnsembleOfOneIsTheMethod(t *testing.T) {
	b, last := builder([]int{0, 4, 8, 12, 16})
	opt := nowcast.Options{Horizon: 30, Threshold: 20, Heavy: 40, Radius: 1, KmPerPx: 1.2}
	p := Single(TREC, 4, false).Prepare(b, last, 15)
	f, _ := b.Field(TREC, last, 4)
	want := nowcast.Forecast(b.Grid(last), f, 60, 170, opt)
	got := p.Forecast(b.Grid(last), 60, 170, opt, false)
	if got.ArrivalMin != want.ArrivalMin || math.Abs(got.SpeedKmh-want.SpeedKmh) > 1e-9 {
		t.Fatalf("ensemble of one: arrival %d speed %.2f, want %d %.2f",
			got.ArrivalMin, got.SpeedKmh, want.ArrivalMin, want.SpeedKmh)
	}
	for m := range want.Series {
		if got.Series[m].DBZ != want.Series[m].DBZ {
			t.Fatalf("minute %d: %v, want %v", m, got.Series[m].DBZ, want.Series[m].DBZ)
		}
	}
}

// The default ensemble sees rain coming toward a point east of the cells.
func TestDefaultForecastsArrival(t *testing.T) {
	b, last := builder([]int{0, 4, 8, 12, 16})
	p := Default().Prepare(b, last, 15)
	if p == nil || p.Used != 5 || !p.HasTrend() || p.Display == nil {
		t.Fatalf("prepared = %+v", p)
	}
	opt := nowcast.Options{Horizon: 60, Threshold: 20, Radius: 1, KmPerPx: 1.2}
	// The cell at x=110+16 reaches x=160 after about 34 px, ~85 minutes:
	// pick a point 12 px ahead of its edge (~30 minutes).
	r := p.Forecast(b.Grid(last), 162, 170, opt, false)
	if r.RainingNow || r.ArrivalMin < 15 || r.ArrivalMin > 45 {
		t.Fatalf("arrival %d min, want ~30", r.ArrivalMin)
	}
	if r.DirectionDeg < 60 || r.DirectionDeg > 120 {
		t.Fatalf("heading %.0f°, want east", r.DirectionDeg)
	}
	if len(r.MotionMembers) != 3 || r.MotionCoherence < 0.9 {
		t.Fatalf("members %+v coherence %.2f, want 3 agreeing", r.MotionMembers, r.MotionCoherence)
	}
	// Over a cell that really moved, moving it explains the last change.
	if c := p.Forecast(b.Grid(last), 126, 170, opt, false); c.MotionGain == nil || *c.MotionGain < 0.5 {
		t.Fatalf("gain over the moving cell = %v, want > 0.5", c.MotionGain)
	}
}

func TestSingleMemberMotion(t *testing.T) {
	b, last := builder([]int{0, 4, 8, 12, 16})
	p := Single(TREC, 4, false).Prepare(b, last, 15)
	opt := nowcast.Options{Horizon: 30, Threshold: 20, Radius: 1, KmPerPx: 1.2}
	r := p.Forecast(b.Grid(last), 126, 170, opt, false)
	if math.Abs(r.MotionCoherence-1) > 1e-9 || len(r.MotionMembers) != 1 ||
		r.MotionMembers[0].Method != TREC || math.Abs(r.MotionMembers[0].SpeedKmh-r.SpeedKmh) > 1e-9 {
		t.Fatalf("coherence %v members %+v speed %v", r.MotionCoherence, r.MotionMembers, r.SpeedKmh)
	}
}

func TestParse(t *testing.T) {
	if !Default().Trend {
		t.Fatal("the default model must use the intensity trend")
	}
	m, err := Parse("ensemble", 6, true)
	if err != nil || len(m.Members) != 3 || m.Members[0].Pairs != 6 || !m.Trend {
		t.Fatalf("ensemble: %+v %v", m, err)
	}
	if m, err := Parse("lk", 4, false); err != nil || len(m.Members) != 1 || m.Members[0].Method != LK {
		t.Fatalf("lk: %+v %v", m, err)
	}
	if _, err := Parse("nope", 4, false); err == nil {
		t.Fatal("unknown model accepted")
	}
}

// A Prepared that went through MarshalBinary forecasts exactly the same.
func TestPreparedRoundTrip(t *testing.T) {
	b, last := builder([]int{0, 2, 5, 9, 14})
	p := Default().Prepare(b, last, 15)
	if len(p.Storms) != 3 {
		t.Fatalf("followed %d storms, want the 3 cells", len(p.Storms))
	}
	data, err := p.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var q Prepared
	if err := q.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if q.Used != p.Used || q.HasTrend() != p.HasTrend() || q.Display.Global != p.Display.Global || len(q.Storms) != len(p.Storms) {
		t.Fatalf("decoded %+v, want %+v", q, p)
	}
	opt := nowcast.Options{Horizon: 60, Threshold: 20, Radius: 1, KmPerPx: 1.2}
	for _, trend := range []bool{false, true} {
		want := p.Forecast(b.Grid(last), 162, 170, opt, trend)
		got := q.Forecast(b.Grid(last), 162, 170, opt, trend)
		if got.ArrivalMin != want.ArrivalMin || got.SpeedKmh != want.SpeedKmh || len(got.Series) != len(want.Series) {
			t.Fatalf("trend=%v: forecast %+v, want %+v", trend, got, want)
		}
		for i := range want.Series {
			if got.Series[i] != want.Series[i] {
				t.Fatalf("trend=%v: series[%d] = %+v, want %+v", trend, i, got.Series[i], want.Series[i])
			}
		}
	}
}

type mapStore map[string]*motion.Field

func (m mapStore) Get(method, id string) (*motion.Field, bool) { f, ok := m[method+id]; return f, ok }
func (m mapStore) Put(method, id string, f *motion.Field)      { m[method+id] = f }

// Named pairs are cached by content: other content at the same times is
// estimated anew, and the shared store serves a fresh Cache.
func TestCacheKeysPairsByContent(t *testing.T) {
	shared := mapStore{}
	b, last := builder([]int{0, 4})
	b.Cache.Shared = shared
	b.PairID = func(int64) string { return "a" }
	fa, _ := b.Field(TREC, last, 1)
	if len(shared) != 1 || shared[TREC+"a"] != fa {
		t.Fatalf("shared store = %v", shared)
	}
	b.PairID = func(int64) string { return "b" }
	if fb, _ := b.Field(TREC, last, 1); fb == fa {
		t.Fatal("a pair with other content reused the cached field")
	}
	// A new process (empty Cache) finds content "a" in the shared store.
	b2, _ := builder([]int{0, 4})
	b2.Cache.Shared = shared
	b2.PairID = func(int64) string { return "a" }
	if f, _ := b2.Field(TREC, last, 1); f != fa {
		t.Fatal("shared field not used")
	}
}

// A cell that formed since the last frame counts as building nearby; one
// steady for the whole history does not.
func TestNearbyStorms(t *testing.T) {
	grids := map[int64]*radar.Grid{}
	for i := range 3 {
		g := cellsAt(0)
		if i == 2 {
			for y := 210; y < 226; y++ {
				for x := 210; x < 226; x++ {
					g.Set(x, y, 45)
				}
			}
		}
		grids[int64(600*(i+1))] = g
	}
	b := &Builder{
		Grid:  func(t int64) *radar.Grid { return grids[t] },
		Prev:  func(t int64) (int64, bool) { return t - 600, grids[t-600] != nil },
		Cache: NewCache(),
	}
	p := Default().Prepare(b, 1800, 15)
	if n := p.Nearby(218, 218, 12); n != 1 {
		t.Errorf("building storms near the new cell = %d, want 1", n)
	}
	if n := p.Nearby(110, 170, 12); n != 0 {
		t.Errorf("building storms near a steady cell = %d, want 0", n)
	}
}
