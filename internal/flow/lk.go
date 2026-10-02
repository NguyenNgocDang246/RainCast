package flow

import (
	"math"

	"raincast/internal/motion"
	"raincast/internal/radar"
)

// LKOptions tunes pyramidal Lucas–Kanade.
type LKOptions struct {
	Levels int
	Radius int // window half-size; the window is (2r+1)²
	Iters  int // refinements per level
	// MinEigen is the smallest structure-tensor eigenvalue, per window
	// pixel, for a solve to be trusted.
	MinEigen  float64
	BlockSize int
}

// DefaultLK suits 10-minute frames at ~1.2 km per pixel.
func DefaultLK() LKOptions {
	return LKOptions{Levels: 4, Radius: 7, Iters: 3, MinEigen: 0.5, BlockSize: 16}
}

// LucasKanade estimates motion prev → cur, minutes apart, as a field in
// pixels per minute. Each pixel's flow is the least-squares fit over the
// window around it; windows without enough texture (flat rain or empty
// sky) are left unsolved and filled from their neighbors.
func LucasKanade(prev, cur *radar.Grid, minutes float64, opt LKOptions) *motion.Field {
	p0, c0 := fromGrid(prev), fromGrid(cur)
	pp, cp := pyramid(p0, opt.Levels), pyramid(c0, opt.Levels)
	var f *flowField
	var ok []bool
	for l := len(pp) - 1; l >= 0; l-- {
		pl, cl := pp[l], cp[l]
		if f == nil {
			f = newFlow(pl.w, pl.h)
		} else {
			f = f.upsample(pl.w, pl.h)
		}
		for range opt.Iters {
			ok = lkStep(f, pl, warp(cl, f), opt)
		}
	}
	return toField(f, p0, c0, ok, opt.BlockSize, minutes)
}

// lkStep adds one least-squares increment to every well-conditioned pixel
// and reports which were.
func lkStep(f *flowField, prev, warped *image, opt LKOptions) []bool {
	ix, iy, it := gradients(prev, warped)
	w, h := f.w, f.h
	n := len(ix)
	prod := func(a, b []float32) []float64 {
		out := make([]float64, n)
		for i := range out {
			out[i] = float64(a[i]) * float64(b[i])
		}
		return out
	}
	r := opt.Radius
	sxx, sxy, syy := boxSum(prod(ix, ix), w, h, r), boxSum(prod(ix, iy), w, h, r), boxSum(prod(iy, iy), w, h, r)
	sxt, syt := boxSum(prod(ix, it), w, h, r), boxSum(prod(iy, it), w, h, r)
	win := float64((2*r + 1) * (2*r + 1))
	ok := make([]bool, n)
	for i := range n {
		a, b, c := sxx[i], sxy[i], syy[i]
		lmin := (a+c)/2 - math.Sqrt((a-c)*(a-c)/4+b*b)
		if lmin < opt.MinEigen*win {
			continue
		}
		det := a*c - b*b
		du := (-c*sxt[i] + b*syt[i]) / det
		dv := (b*sxt[i] - a*syt[i]) / det
		// A step longer than the window is a bad fit, not motion.
		if math.Abs(du) > float64(r) || math.Abs(dv) > float64(r) {
			continue
		}
		f.u[i] += float32(du)
		f.v[i] += float32(dv)
		ok[i] = true
	}
	return ok
}

// boxSum sums p over the (2r+1)² window around every pixel, via an
// integral image.
func boxSum(p []float64, w, h, r int) []float64 {
	iw := w + 1
	s := make([]float64, iw*(h+1))
	for y := range h {
		var row float64
		for x := range w {
			row += p[y*w+x]
			s[(y+1)*iw+x+1] = s[y*iw+x+1] + row
		}
	}
	out := make([]float64, w*h)
	for y := range h {
		y0, y1 := max(y-r, 0), min(y+r+1, h)
		for x := range w {
			x0, x1 := max(x-r, 0), min(x+r+1, w)
			out[y*w+x] = s[y1*iw+x1] - s[y0*iw+x1] - s[y1*iw+x0] + s[y0*iw+x0]
		}
	}
	return out
}
