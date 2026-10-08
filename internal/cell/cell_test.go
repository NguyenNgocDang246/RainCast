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
