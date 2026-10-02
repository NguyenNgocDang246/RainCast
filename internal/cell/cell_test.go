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

// Two cells 20 px apart both move 14 px east: the leading edge of the
// first lands next to where the second was. Nearest neighbor sends both to
// the second cell; the optimal assignment keeps them apart.
func TestHungarianBeatsNearestOnCloseCells(t *testing.T) {
	prev, cur := radar.NewGrid(256, 128), radar.NewGrid(256, 128)
	disc(prev, 100, 64, 6, 50)
	disc(prev, 120, 64, 6, 50)
	disc(cur, 114, 64, 6, 50)
	disc(cur, 134, 64, 6, 50)
	pc, cc := Segment(prev, 30, 16), Segment(cur, 30, 16)
	if len(pc) != 2 || len(cc) != 2 {
		t.Fatalf("cells: %d, %d", len(pc), len(cc))
	}
	right := func(pairs []Pair) bool {
		ok := 0
		for _, p := range pairs {
			if math.Abs(cc[p.Cur].X-pc[p.Prev].X-14) < 1 {
				ok++
			}
		}
		return ok == 2 && len(pairs) == 2
	}
	if right(MatchNearest(pc, cc, nil, 10, 20)) {
		t.Error("nearest neighbor unexpectedly got it right; the test no longer shows the difference")
	}
	if pairs := MatchHungarian(pc, cc, nil, 10, 20); !right(pairs) {
		t.Errorf("hungarian pairs %v are wrong", pairs)
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

// Two storms crossing the area flow at different headings: the cell field
// moves each with its own velocity.
func TestFieldFollowsEachCell(t *testing.T) {
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
	for name, m := range map[string]Matcher{"nearest": MatchNearest, "hungarian": MatchHungarian} {
		f := Field(frames, pairs, 10, DefaultOptions(m))
		a, b := f.At(109, 100), f.At(260, 232)
		if math.Abs(a.DX-0.3) > 0.06 || math.Abs(a.DY) > 0.06 {
			t.Errorf("%s: east cell moves %+v, want (0.3, 0)", name, a)
		}
		if math.Abs(b.DX) > 0.06 || math.Abs(b.DY-0.4) > 0.06 {
			t.Errorf("%s: south cell moves %+v, want (0, 0.4)", name, b)
		}
	}
	// Hybrid keeps the base motion away from cells.
	base := motion.FromBlocks(32, 12, 12, func() []motion.Vector {
		v := make([]motion.Vector, 144)
		for i := range v {
			v[i] = motion.Vector{DX: -0.1}
		}
		return v
	}(), func() []bool {
		v := make([]bool, 144)
		for i := range v {
			v[i] = true
		}
		return v
	}())
	h := Hybrid(frames, pairs, base, 10, DefaultOptions(MatchHungarian))
	if v := h.At(20, 350); math.Abs(v.DX+0.1) > 1e-9 {
		t.Errorf("hybrid away from cells = %+v, want the base motion", v)
	}
	if v := h.At(109, 100); math.Abs(v.DX-0.3) > 0.1 {
		t.Errorf("hybrid at the east cell = %+v, want the cell's motion", v)
	}
}
