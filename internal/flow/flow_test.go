package flow

import (
	"math"
	"math/rand/v2"
	"testing"

	"raincast/internal/motion"
	"raincast/internal/radar"
)

// rainfield draws n random rain cells, shifted by (sx, sy).
func rainfield(w, h, n int, sx, sy float64, seed uint64) *radar.Grid {
	r := rand.New(rand.NewPCG(seed, 1))
	type cell struct{ x, y, rad, peak float64 }
	cells := make([]cell, n)
	for i := range cells {
		cells[i] = cell{r.Float64() * float64(w), r.Float64() * float64(h), 8 + r.Float64()*25, 25 + r.Float64()*30}
	}
	g := radar.NewGrid(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := float64(radar.MinDBZ)
			for _, c := range cells {
				dx, dy := float64(x)-(c.x+sx), float64(y)-(c.y+sy)
				if d := math.Sqrt(dx*dx+dy*dy) / c.rad; d < 1.3 {
					v = math.Max(v, c.peak-18*d)
				}
			}
			g.Set(x, y, float32(math.Round(v*2)/2))
		}
	}
	return g
}

// meanRainVector averages the field over blocks where cur has rain, in
// pixels per frame.
func meanRainVector(f *motion.Field, cur *radar.Grid, minutes float64) (dx, dy float64) {
	n := 0
	for y := 64; y < cur.H-64; y += 8 {
		for x := 64; x < cur.W-64; x += 8 {
			if cur.At(x, y) < 20 {
				continue
			}
			v := f.At(float64(x), float64(y))
			dx += v.DX * minutes
			dy += v.DY * minutes
			n++
		}
	}
	return dx / float64(n), dy / float64(n)
}

func TestRecoverShift(t *testing.T) {
	methods := map[string]func(prev, cur *radar.Grid, minutes float64) *motion.Field{
		"horn-schunck": func(p, c *radar.Grid, m float64) *motion.Field { return HornSchunck(p, c, m, DefaultHS()) },
		"lucas-kanade": func(p, c *radar.Grid, m float64) *motion.Field { return LucasKanade(p, c, m, DefaultLK()) },
	}
	for _, shift := range [][2]float64{{5, -3}, {12, 7}} {
		prev := rainfield(384, 384, 40, 0, 0, 3)
		cur := rainfield(384, 384, 40, shift[0], shift[1], 3)
		for name, est := range methods {
			f := est(prev, cur, 10)
			dx, dy := meanRainVector(f, cur, 10)
			if math.Abs(dx-shift[0]) > 0.8 || math.Abs(dy-shift[1]) > 0.8 {
				t.Errorf("%s: shift %v recovered as (%.2f, %.2f)", name, shift, dx, dy)
			}
		}
	}
}

func TestNoRainGivesZeroField(t *testing.T) {
	empty := radar.NewGrid(128, 128)
	for _, f := range []*motion.Field{
		HornSchunck(empty, empty, 10, DefaultHS()),
		LucasKanade(empty, empty, 10, DefaultLK()),
	} {
		if f.Reliable() {
			t.Error("field reliable without any rain")
		}
		if v := f.At(64, 64); v.DX != 0 || v.DY != 0 {
			t.Errorf("motion %+v without rain", v)
		}
	}
}

func BenchmarkHornSchunck768(b *testing.B) {
	prev, cur := rainfield(768, 768, 120, 0, 0, 7), rainfield(768, 768, 120, 5, -3, 7)
	for b.Loop() {
		HornSchunck(prev, cur, 10, DefaultHS())
	}
}

func BenchmarkLucasKanade768(b *testing.B) {
	prev, cur := rainfield(768, 768, 120, 0, 0, 7), rainfield(768, 768, 120, 5, -3, 7)
	for b.Loop() {
		LucasKanade(prev, cur, 10, DefaultLK())
	}
}
