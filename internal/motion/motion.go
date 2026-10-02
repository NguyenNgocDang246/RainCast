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
	return Options{BlockSize: 32, Search: 16, RainDBZ: 15, MinFrac: 0.05, MinCorr: 0.5, Workers: runtime.GOMAXPROCS(0)}
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

	m := newMatcher(prev, cur, opt)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range max(opt.Workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := m.buffers()
			for i := range jobs {
				bx, by := i%f.BW, i/f.BW
				if dx, dy, ok := m.match(bx*bs, by*bs, buf); ok {
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

// matcher holds both frames as matching signal, prepared once per frame
// pair and shared read-only by the workers. prev is padded by Search on
// every side (outside the grid is no echo) and has integral images, so each
// candidate window's mean and spread cost O(1) instead of a pass over it.
type matcher struct {
	opt     Options
	cur     []float64 // intensity of cur, cur.W wide
	curW    int
	pad     []float64 // intensity of prev, padded
	padW    int
	sum     []float64 // integral image of pad, (padW+1) wide
	sum2    []float64 // integral image of pad²
	curRain []bool    // cur pixel at or above RainDBZ
}

func newMatcher(prev, cur *radar.Grid, opt Options) *matcher {
	s := opt.Search
	m := &matcher{opt: opt, curW: cur.W, cur: make([]float64, len(cur.Data)), curRain: make([]bool, len(cur.Data))}
	for i, v := range cur.Data {
		m.cur[i] = intensity(v)
		m.curRain[i] = v >= opt.RainDBZ
	}
	m.padW = prev.W + 2*s
	padH := prev.H + 2*s
	m.pad = make([]float64, m.padW*padH)
	for y := 0; y < prev.H; y++ {
		row := m.pad[(y+s)*m.padW+s:]
		for x := 0; x < prev.W; x++ {
			row[x] = intensity(prev.Data[y*prev.W+x])
		}
	}
	iw := m.padW + 1
	m.sum = make([]float64, iw*(padH+1))
	m.sum2 = make([]float64, iw*(padH+1))
	for y := 0; y < padH; y++ {
		var rs, rs2 float64
		for x := 0; x < m.padW; x++ {
			v := m.pad[y*m.padW+x]
			rs += v
			rs2 += v * v
			m.sum[(y+1)*iw+x+1] = m.sum[y*iw+x+1] + rs
			m.sum2[(y+1)*iw+x+1] = m.sum2[y*iw+x+1] + rs2
		}
	}
	return m
}

// matchBuffers is per-worker scratch space.
type matchBuffers struct {
	tpl  []float64 // the block, mean removed
	corr []float64
}

func (m *matcher) buffers() *matchBuffers {
	bs, w := m.opt.BlockSize, 2*m.opt.Search+1
	return &matchBuffers{tpl: make([]float64, bs*bs), corr: make([]float64, w*w)}
}

// match finds where the block at (x0, y0) in cur came from in prev. The
// returned (dx, dy) is the displacement prev → cur in pixels.
func (m *matcher) match(x0, y0 int, b *matchBuffers) (dx, dy float64, ok bool) {
	opt := m.opt
	bs := opt.BlockSize
	n := float64(bs * bs)
	rainy := 0
	var tSum float64
	for y := y0; y < y0+bs; y++ {
		row := y * m.curW
		for x := x0; x < x0+bs; x++ {
			if m.curRain[row+x] {
				rainy++
			}
			tSum += m.cur[row+x]
		}
	}
	if float64(rainy) < opt.MinFrac*n {
		return 0, 0, false
	}
	tMean := tSum / n
	var tVar float64
	k := 0
	for y := y0; y < y0+bs; y++ {
		row := m.cur[y*m.curW+x0 : y*m.curW+x0+bs]
		for _, v := range row {
			d := v - tMean
			b.tpl[k] = d
			tVar += d * d
			k++
		}
	}
	tStd := math.Sqrt(tVar / n)
	if tStd == 0 {
		return 0, 0, false
	}

	s := opt.Search
	w := 2*s + 1
	iw := m.padW + 1
	best, bi, bj := math.Inf(-1), 0, 0
	for j := -s; j <= s; j++ {
		for i := -s; i <= s; i++ {
			// The window in prev is (x0-i, y0-j) in grid coordinates,
			// (x0-i+s, y0-j+s) in the padded image.
			px, py := x0-i+s, y0-j+s
			a, c := py*iw+px, (py+bs)*iw+px
			sum := m.sum[c+bs] - m.sum[c] - m.sum[a+bs] + m.sum[a]
			sum2 := m.sum2[c+bs] - m.sum2[c] - m.sum2[a+bs] + m.sum2[a]
			wMean := sum / n
			wVar := sum2/n - wMean*wMean
			cc := math.Inf(-1)
			if wVar > 1e-9 {
				// Σ (t−t̄)(w−w̄) = Σ (t−t̄)·w, since Σ (t−t̄) = 0.
				var dot float64
				k := 0
				for y := 0; y < bs; y++ {
					row := m.pad[(py+y)*m.padW+px : (py+y)*m.padW+px+bs]
					tr := b.tpl[k : k+bs]
					for x, v := range row {
						dot += tr[x] * v
					}
					k += bs
				}
				cc = dot / (n * tStd * math.Sqrt(wVar))
			}
			b.corr[(j+s)*w+(i+s)] = cc
			// Prefer the smallest displacement on ties to stay stable over flat areas.
			if cc > best || (cc == best && i*i+j*j < bi*bi+bj*bj) {
				best, bi, bj = cc, i, j
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
		return b.corr[(j+s)*w+(i+s)]
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

// FromBlocks builds a field from per-block vectors (pixels per minute),
// where valid marks blocks that were actually measured; the rest are filled
// the same way as block matching fills them.
func FromBlocks(blockSize, bw, bh int, v []Vector, valid []bool) *Field {
	f := &Field{BlockSize: blockSize, BW: bw, BH: bh,
		V: append([]Vector(nil), v...), Valid: append([]bool(nil), valid...)}
	f.finish()
	return f
}
