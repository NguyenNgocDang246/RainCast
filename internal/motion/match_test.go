package motion

import (
	"math"
	"math/rand/v2"
	"testing"

	"raincast/internal/radar"
)

// rainfield draws n random rain cells over a w×h grid, shifted by (sx, sy).
func rainfield(w, h, n int, sx, sy float64, seed uint64) *radar.Grid {
	r := rand.New(rand.NewPCG(seed, 1))
	type cell struct{ x, y, rad, peak float64 }
	cells := make([]cell, n)
	for i := range cells {
		cells[i] = cell{r.Float64() * float64(w), r.Float64() * float64(h), 6 + r.Float64()*30, 25 + r.Float64()*30}
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
			// Quantize like the palette does.
			g.Set(x, y, float32(math.Round(v*2)/2))
		}
	}
	return g
}

// matchBlockRef is the original, straightforward block matcher: the
// optimized one must agree with it.
func matchBlockRef(prev, cur *radar.Grid, x0, y0 int, opt Options) (dx, dy float64, ok bool) {
	bs := opt.BlockSize
	tpl := make([]float64, 0, bs*bs)
	rainy := 0
	for y := y0; y < y0+bs; y++ {
		for x := x0; x < x0+bs; x++ {
			v := cur.At(x, y)
			if v >= opt.RainDBZ {
				rainy++
			}
			tpl = append(tpl, intensity(v))
		}
	}
	if float64(rainy) < opt.MinFrac*float64(bs*bs) {
		return 0, 0, false
	}
	tMean, tStd := meanStd(tpl)
	if tStd == 0 {
		return 0, 0, false
	}
	s := opt.Search
	w := 2*s + 1
	corr := make([]float64, w*w)
	best, bi, bj := math.Inf(-1), 0, 0
	win := make([]float64, bs*bs)
	for j := -s; j <= s; j++ {
		for i := -s; i <= s; i++ {
			k := 0
			for y := y0; y < y0+bs; y++ {
				for x := x0; x < x0+bs; x++ {
					win[k] = intensity(prev.At(x-i, y-j))
					k++
				}
			}
			wMean, wStd := meanStd(win)
			c := math.Inf(-1)
			if wStd > 0 {
				var sum float64
				for k := range tpl {
					sum += (tpl[k] - tMean) * (win[k] - wMean)
				}
				c = sum / (float64(len(tpl)) * tStd * wStd)
			}
			corr[(j+s)*w+(i+s)] = c
			if c > best || (c == best && i*i+j*j < bi*bi+bj*bj) {
				best, bi, bj = c, i, j
			}
		}
	}
	if best < opt.MinCorr {
		return 0, 0, false
	}
	at := func(i, j int) float64 {
		if i < -s || i > s || j < -s || j > s {
			return math.Inf(-1)
		}
		return corr[(j+s)*w+(i+s)]
	}
	dx = float64(bi) + subpixel(at(bi-1, bj), best, at(bi+1, bj))
	dy = float64(bj) + subpixel(at(bi, bj-1), best, at(bi, bj+1))
	return dx, dy, true
}

func TestFastMatchAgreesWithReference(t *testing.T) {
	opt := DefaultOptions()
	for seed := range uint64(4) {
		prev := rainfield(256, 256, 25, 0, 0, seed)
		cur := rainfield(256, 256, 25, 4.3, -2.6, seed)
		m := newMatcher(prev, cur, opt)
		var compared, differ int
		for by := 0; by < 256/opt.BlockSize; by++ {
			for bx := 0; bx < 256/opt.BlockSize; bx++ {
				x0, y0 := bx*opt.BlockSize, by*opt.BlockSize
				rdx, rdy, rok := matchBlockRef(prev, cur, x0, y0, opt)
				dx, dy, ok := m.match(x0, y0, m.buffers())
				if ok != rok {
					t.Fatalf("seed %d block (%d,%d): ok %v, reference %v", seed, bx, by, ok, rok)
				}
				if !ok {
					continue
				}
				compared++
				if math.Abs(dx-rdx) > 1e-6 || math.Abs(dy-rdy) > 1e-6 {
					differ++
					t.Logf("seed %d block (%d,%d): (%.4f,%.4f) vs reference (%.4f,%.4f)", seed, bx, by, dx, dy, rdx, rdy)
				}
			}
		}
		if compared == 0 {
			t.Fatalf("seed %d: no block matched", seed)
		}
		// Rounding can flip an exact tie between two shifts; anything more
		// means the fast path is wrong.
		if differ > compared/20 {
			t.Fatalf("seed %d: %d of %d blocks differ from the reference", seed, differ, compared)
		}
	}
}

func BenchmarkEstimate768(b *testing.B) {
	prev := rainfield(768, 768, 120, 0, 0, 7)
	cur := rainfield(768, 768, 120, 5, -3, 7)
	opt := DefaultOptions()
	b.ResetTimer()
	for b.Loop() {
		Estimate(prev, cur, 10, opt)
	}
}

// meanStd is the reference normalization the fast matcher must agree with.
func meanStd(xs []float64) (mean, std float64) {
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	for _, x := range xs {
		std += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(std / float64(len(xs)))
}
