// Package motion estimates how radar echoes move between two frames using
// block matching with normalized cross-correlation.
package motion

import (
	"math"
	"runtime"
	"sort"
	"sync"

	"raincast/internal/radar"
)

// Vector is a displacement in pixels per minute; +X is east, +Y is south.
type Vector struct{ DX, DY float64 }

// Options tunes the block matcher.
type Options struct {
	BlockSize int     // block edge in pixels
	Search    int     // max displacement searched, in pixels per frame pair
	RainDBZ   float32 // echoes at or above this count as rain
	MinFrac   float64 // min share of rainy pixels for a block to be matched
	MinCorr   float64 // min correlation peak to accept a match
	Workers   int
}

// DefaultOptions suit 10-minute frames at ~1.2 km/pixel.
func DefaultOptions() Options {
	return Options{BlockSize: 32, Search: 16, RainDBZ: 15, MinFrac: 0.05, MinCorr: 0.5, Workers: runtime.NumCPU()}
}

// Field is a per-block motion field.
type Field struct {
	BlockSize int
	BW, BH    int
	V         []Vector
	Valid     []bool // true where the block itself was matched
	Global    Vector // median of valid blocks
	NValid    int
}

// Reliable reports whether any block was matched.
func (f *Field) Reliable() bool { return f.NValid > 0 }

// At returns the vector at pixel (x, y), bilinearly interpolated between block centers.
func (f *Field) At(x, y float64) Vector {
	if f.BW == 0 || f.BH == 0 {
		return f.Global
	}
	bs := float64(f.BlockSize)
	fx := clamp(x/bs-0.5, 0, float64(f.BW-1))
	fy := clamp(y/bs-0.5, 0, float64(f.BH-1))
	x0, y0 := int(fx), int(fy)
	x1, y1 := min(x0+1, f.BW-1), min(y0+1, f.BH-1)
	tx, ty := fx-float64(x0), fy-float64(y0)
	v00, v10 := f.V[y0*f.BW+x0], f.V[y0*f.BW+x1]
	v01, v11 := f.V[y1*f.BW+x0], f.V[y1*f.BW+x1]
	lerp := func(a, b, c, d float64) float64 {
		return (a*(1-tx)+b*tx)*(1-ty) + (c*(1-tx)+d*tx)*ty
	}
	return Vector{
		DX: lerp(v00.DX, v10.DX, v01.DX, v11.DX),
		DY: lerp(v00.DY, v10.DY, v01.DY, v11.DY),
	}
}

// Estimate matches blocks of cur against prev. minutes is the time between
// the two frames; the result is in pixels per minute.
func Estimate(prev, cur *radar.Grid, minutes float64, opt Options) *Field {
	bs := opt.BlockSize
	f := &Field{BlockSize: bs, BW: cur.W / bs, BH: cur.H / bs}
	n := f.BW * f.BH
	f.V = make([]Vector, n)
	f.Valid = make([]bool, n)

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range max(opt.Workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				bx, by := i%f.BW, i/f.BW
				if dx, dy, ok := matchBlock(prev, cur, bx*bs, by*bs, opt); ok {
					f.V[i] = Vector{dx / minutes, dy / minutes}
					f.Valid[i] = true
				}
			}
		}()
	}
	for i := range n {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	f.finish()
	return f
}

// matchBlock finds where the block at (x0, y0) in cur came from in prev.
// The returned (dx, dy) is the displacement prev → cur in pixels.
func matchBlock(prev, cur *radar.Grid, x0, y0 int, opt Options) (dx, dy float64, ok bool) {
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
			// Prefer the smallest displacement on ties to stay stable over flat areas.
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

// intensity maps dBZ to a matching signal that ignores clutter below 10 dBZ.
func intensity(v float32) float64 {
	if v < 10 {
		return 0
	}
	return float64(min(v, 65) - 10)
}

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

// subpixel fits a parabola through three samples around a peak.
func subpixel(l, c, r float64) float64 {
	if math.IsInf(l, -1) || math.IsInf(r, -1) {
		return 0
	}
	d := l - 2*c + r
	if d >= 0 {
		return 0
	}
	return clamp(0.5*(l-r)/d, -0.5, 0.5)
}

// finish smooths matched vectors and fills unmatched blocks.
func (f *Field) finish() {
	var xs, ys []float64
	for i, ok := range f.Valid {
		if ok {
			xs = append(xs, f.V[i].DX)
			ys = append(ys, f.V[i].DY)
		}
	}
	f.NValid = len(xs)
	if f.NValid == 0 {
		return
	}
	f.Global = Vector{median(xs), median(ys)}

	// 3×3 median filter over valid neighbors removes outliers.
	smoothed := make([]Vector, len(f.V))
	for by := 0; by < f.BH; by++ {
		for bx := 0; bx < f.BW; bx++ {
			i := by*f.BW + bx
			if !f.Valid[i] {
				continue
			}
			xs, ys = xs[:0], ys[:0]
			for ny := by - 1; ny <= by+1; ny++ {
				for nx := bx - 1; nx <= bx+1; nx++ {
					if nx < 0 || ny < 0 || nx >= f.BW || ny >= f.BH || !f.Valid[ny*f.BW+nx] {
						continue
					}
					xs = append(xs, f.V[ny*f.BW+nx].DX)
					ys = append(ys, f.V[ny*f.BW+nx].DY)
				}
			}
			smoothed[i] = Vector{median(xs), median(ys)}
		}
	}

	// Unmatched blocks: inverse-distance weighting from matched ones,
	// blended toward the global vector when they are far away.
	const reach = 4.0 // blocks
	for by := 0; by < f.BH; by++ {
		for bx := 0; bx < f.BW; bx++ {
			i := by*f.BW + bx
			if f.Valid[i] {
				f.V[i] = smoothed[i]
				continue
			}
			wsum := 1 / (reach * reach) // weight of the global vector
			vx, vy := f.Global.DX*wsum, f.Global.DY*wsum
			for j, ok := range f.Valid {
				if !ok {
					continue
				}
				ddx, ddy := float64(j%f.BW-bx), float64(j/f.BW-by)
				w := 1 / (ddx*ddx + ddy*ddy)
				wsum += w
				vx += smoothed[j].DX * w
				vy += smoothed[j].DY * w
			}
			f.V[i] = Vector{vx / wsum, vy / wsum}
		}
	}
}

// Average combines fields of the same shape with equal weights.
func Average(fields ...*Field) *Field {
	w := make([]float64, len(fields))
	for i := range w {
		w[i] = 1
	}
	return WeightedAverage(fields, w)
}

// WeightedAverage combines fields of the same shape. Each block averages
// only the fields that matched it, weighted by weights[i]. Over several
// frame pairs, back-and-forth apparent motion (a cell pulsing between two
// spots) cancels out instead of being extrapolated as a steady drift.
func WeightedAverage(fields []*Field, weights []float64) *Field {
	if len(fields) == 0 {
		return nil
	}
	if len(fields) == 1 {
		return fields[0]
	}
	base := fields[0]
	out := &Field{BlockSize: base.BlockSize, BW: base.BW, BH: base.BH,
		V: make([]Vector, len(base.V)), Valid: make([]bool, len(base.V))}
	for i := range out.V {
		var sx, sy, sw float64
		for k, f := range fields {
			if f.Valid[i] {
				sx += f.V[i].DX * weights[k]
				sy += f.V[i].DY * weights[k]
				sw += weights[k]
			}
		}
		if sw > 0 {
			out.V[i] = Vector{sx / sw, sy / sw}
			out.Valid[i] = true
		}
	}
	out.finish()
	return out
}

func median(xs []float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
