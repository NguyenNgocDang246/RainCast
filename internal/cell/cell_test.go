package cell

import (
	"math"
	"testing"

	"raincast/internal/motion"
	"raincast/internal/radar"
)

// disc draws a round cell of radius r and peak dBZ at (cx, cy).
func disc(g *radar.Grid, cx, cy, r, peak float64) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			if d := math.Hypot(float64(x)-cx, float64(y)-cy); d <= r {
				g.Set(x, y, max(g.At(x, y), float32(peak-10*d/r)))
			}
		}
	}
}

func still(w, h int) *motion.Field {
	bw, bh := w/32, h/32
	return motion.FromBlocks(32, bw, bh, make([]motion.Vector, bw*bh), make([]bool, bw*bh))
}

func TestSegment(t *testing.T) {
	g := radar.NewGrid(200, 200)
	disc(g, 50, 60, 8, 50)
	disc(g, 140, 120, 10, 45)
	disc(g, 20, 180, 1.5, 45) // too small
	cells := Segment(g, 30, 16)
	if len(cells) != 2 {
		t.Fatalf("got %d cells, want 2", len(cells))
	}
	for _, want := range [][2]float64{{50, 60}, {140, 120}} {
		found := false
		for _, c := range cells {
			if math.Hypot(c.X-want[0], c.Y-want[1]) < 0.5 {
				found = true
			}
		}
		if !found {
			t.Errorf("no cell centered at %v: %+v", want, cells)
		}
	}
}

func TestHungarianOptimal(t *testing.T) {
	c := [][]float64{{4, 1, 3}, {2, 0, 5}, {3, 2, 2}}
	a := hungarian(c)
	total := 0.0
	for i, j := range a {
		total += c[i][j]
	}
	if total != 5 { // 1 + 2 + 2
		t.Fatalf("assignment %v costs %v, want 5", a, total)
	}
}

func TestKalmanVelocity(t *testing.T) {
	xs, ys := []float64{}, []float64{}
	noise := []float64{0.8, -1.1, 0.4, 1.0, -0.6, 0.2}
	for i := range 6 {
		xs = append(xs, 100+float64(i)*4+noise[i])
		ys = append(ys, 50-float64(i)*2-noise[5-i])
	}
	vx, vy := trackVelocity(xs, ys, 10, 0, 0)
	if math.Abs(vx-0.4) > 0.08 || math.Abs(vy+0.2) > 0.08 {
		t.Fatalf("velocity (%.3f, %.3f) px/min, want (0.4, -0.2)", vx, vy)
	}
}

// Two storms crossing the area flow at different headings: each tracked
// cell gets its own velocity.
func TestTrackFollowsEachCell(t *testing.T) {
	var frames []*radar.Grid
	var pairs []*motion.Field
	for k := range 4 {
		g := radar.NewGrid(384, 384)
		disc(g, 100+3*float64(k), 100, 10, 50) // east, 3 px per frame
		disc(g, 260, 220+4*float64(k), 10, 50) // south, 4 px per frame
		frames = append(frames, g)
		if k > 0 {
			pairs = append(pairs, still(384, 384))
		}
	}
	cells, vel := Track(frames, pairs, 10, DefaultOptions(MatchHungarian))
	if len(cells) != 2 {
		t.Fatalf("tracked %d cells, want 2", len(cells))
	}
	for i, c := range cells {
		want := motion.Vector{DX: 0.3}
		if c.X > 200 {
			want = motion.Vector{DY: 0.4}
		}
		if math.Abs(vel[i].DX-want.DX) > 0.06 || math.Abs(vel[i].DY-want.DY) > 0.06 {
			t.Errorf("cell at (%.0f, %.0f) moves %+v, want %+v", c.X, c.Y, vel[i], want)
		}
	}
}

func frames(n int, draw func(k int, g *radar.Grid)) []*radar.Grid {
	out := make([]*radar.Grid, n)
	for k := range out {
		out[k] = radar.NewGrid(256, 256)
		draw(k, out[k])
	}
	return out
}

func lifeOpt() Options { return DefaultOptions(MatchHungarian) }

// One elongated cell breaks into two: both pieces are splits of it.
func TestLifecycleSplit(t *testing.T) {
	fs := frames(2, func(k int, g *radar.Grid) {
		if k == 0 {
			disc(g, 100, 100, 9, 50)
			disc(g, 114, 100, 9, 50)
		} else {
			disc(g, 94, 100, 7, 50)
			disc(g, 120, 100, 7, 50)
		}
	})
	s := Lifecycle(fs, still(256, 256), 10, lifeOpt())
	if len(s) != 2 || !s[0].Split || !s[1].Split || s[0].New || s[0].Merged {
		t.Fatalf("storms %+v, want two splits", flags(s))
	}
}

// Two cells flow into one.
func TestLifecycleMerge(t *testing.T) {
	fs := frames(2, func(k int, g *radar.Grid) {
		if k == 0 {
			disc(g, 94, 100, 7, 50)
			disc(g, 120, 100, 7, 50)
		} else {
			disc(g, 100, 100, 9, 50)
			disc(g, 114, 100, 9, 50)
		}
	})
	s := Lifecycle(fs, still(256, 256), 10, lifeOpt())
	if len(s) != 1 || !s[0].Merged || s[0].Split || s[0].New {
		t.Fatalf("storms %+v, want one merge", flags(s))
	}
}

// A cell moving east along the field and strengthening 4 dBZ per frame is
// followed back through every frame, growing at 0.4 dBZ/min; a cell
// appearing in the last frame is new.
func TestLifecycleGrowthAndBirth(t *testing.T) {
	fs := frames(3, func(k int, g *radar.Grid) {
		disc(g, 60+5*float64(k), 80, 8, 40+4*float64(k))
		if k == 2 {
			disc(g, 180, 180, 6, 45)
		}
	})
	east := motion.FromBlocks(32, 8, 8, func() []motion.Vector {
		v := make([]motion.Vector, 64)
		for i := range v {
			v[i] = motion.Vector{DX: 0.5}
		}
		return v
	}(), func() []bool {
		v := make([]bool, 64)
		for i := range v {
			v[i] = true
		}
		return v
	}())
	s := Lifecycle(fs, east, 10, lifeOpt())
	if len(s) != 2 {
		t.Fatalf("%d storms, want 2", len(s))
	}
	grow, born := s[0], s[1]
	if grow.X > born.X {
		grow, born = born, grow
	}
	if grow.Age != 3 || grow.New || math.Abs(grow.RateDBZ-0.4) > 0.05 || grow.Decaying {
		t.Errorf("growing cell %+v, want age 3 at 0.4 dBZ/min", flags([]Storm{grow}))
	}
	if !born.New || born.Age != 1 {
		t.Errorf("new cell %+v, want new", flags([]Storm{born}))
	}

	// The trend follows the storms where they cover a block.
	tr := &motion.Trend{BlockSize: 32, BW: 8, BH: 8, R: make([]float64, 64)}
	adj := AdjustTrend(tr, s, 256)
	if r := adj.R[(80/32)*8+70/32]; math.Abs(r-0.4) > 0.05 {
		t.Errorf("trend at the growing cell %.2f, want ≈ 0.4", r)
	}
	if r := adj.R[(180/32)*8+180/32]; r < initRate-1e-9 {
		t.Errorf("trend at the new cell %.2f, want at least %.2f", r, initRate)
	}
	if adj.R[0] != 0 || tr.R[(180/32)*8+180/32] != 0 {
		t.Error("blocks without storms changed, or the input trend was modified")
	}
}

type flagView struct {
	X, Y                    float64
	Age                     int
	New, Merged, Split, Dec bool
	Rate                    float64
}

func flags(s []Storm) []flagView {
	var out []flagView
	for _, x := range s {
		out = append(out, flagView{x.X, x.Y, x.Age, x.New, x.Merged, x.Split, x.Decaying, x.RateDBZ})
	}
	return out
}
