// Package flow estimates dense optical flow between radar frames
// (Horn–Schunck and pyramidal Lucas–Kanade) and turns it into a
// motion.Field, so it plugs into the same nowcast as block matching.
package flow

import (
	"math"

	"raincast/internal/motion"
	"raincast/internal/radar"
)

// image is a single-channel float image; outside it reads as 0 (no echo).
type image struct {
	w, h int
	p    []float32
}

func newImage(w, h int) *image { return &image{w: w, h: h, p: make([]float32, w*h)} }

// fromGrid maps dBZ to a matching signal that ignores clutter below 10 dBZ,
// like the block matcher does.
func fromGrid(g *radar.Grid) *image {
	im := newImage(g.W, g.H)
	for i, v := range g.Data {
		if v >= 10 {
			im.p[i] = min(v, 65) - 10
		}
	}
	return im
}

func (im *image) at(x, y int) float32 {
	if x < 0 || y < 0 || x >= im.w || y >= im.h {
		return 0
	}
	return im.p[y*im.w+x]
}

// sample reads im at a fractional position, bilinearly.
func (im *image) sample(x, y float32) float32 {
	x0, y0 := int(math.Floor(float64(x))), int(math.Floor(float64(y)))
	tx, ty := x-float32(x0), y-float32(y0)
	a := im.at(x0, y0)*(1-tx) + im.at(x0+1, y0)*tx
	b := im.at(x0, y0+1)*(1-tx) + im.at(x0+1, y0+1)*tx
	return a*(1-ty) + b*ty
}

// half blurs with [1 2 1]/4 in both directions and keeps every other pixel.
func (im *image) half() *image {
	out := newImage((im.w+1)/2, (im.h+1)/2)
	k := [3]float32{0.25, 0.5, 0.25}
	for y := range out.h {
		for x := range out.w {
			var s float32
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					s += k[dy+1] * k[dx+1] * im.at(2*x+dx, 2*y+dy)
				}
			}
			out.p[y*out.w+x] = s
		}
	}
	return out
}

// pyramid returns up to levels images, finest first.
func pyramid(im *image, levels int) []*image {
	out := []*image{im}
	for len(out) < levels && out[len(out)-1].w > 16 {
		out = append(out, out[len(out)-1].half())
	}
	return out
}

// flowField is a per-pixel displacement prev → cur, in pixels.
type flowField struct {
	w, h int
	u, v []float32
}

func newFlow(w, h int) *flowField {
	return &flowField{w: w, h: h, u: make([]float32, w*h), v: make([]float32, w*h)}
}

// upsample doubles the flow onto the next finer level of size w×h.
func (f *flowField) upsample(w, h int) *flowField {
	out := newFlow(w, h)
	for y := range h {
		for x := range w {
			sx, sy := min(x/2, f.w-1), min(y/2, f.h-1)
			out.u[y*w+x] = 2 * f.u[sy*f.w+sx]
			out.v[y*w+x] = 2 * f.v[sy*f.w+sx]
		}
	}
	return out
}

// warp returns cur sampled at x + flow(x): cur moved back onto prev.
func warp(cur *image, f *flowField) *image {
	out := newImage(cur.w, cur.h)
	for y := range cur.h {
		for x := range cur.w {
			i := y*cur.w + x
			out.p[i] = cur.sample(float32(x)+f.u[i], float32(y)+f.v[i])
		}
	}
	return out
}

// gradients returns central differences of (a+b)/2 and the temporal
// difference b−a.
func gradients(a, b *image) (ix, iy, it []float32) {
	n := a.w * a.h
	ix, iy, it = make([]float32, n), make([]float32, n), make([]float32, n)
	avg := func(x, y int) float32 { return (a.at(x, y) + b.at(x, y)) / 2 }
	for y := range a.h {
		for x := range a.w {
			i := y*a.w + x
			ix[i] = (avg(x+1, y) - avg(x-1, y)) / 2
			iy[i] = (avg(x, y+1) - avg(x, y-1)) / 2
			it[i] = b.p[i] - a.p[i]
		}
	}
	return ix, iy, it
}

// toField averages the flow over blockSize blocks where either frame has
// echo, since optical flow sees nothing in empty sky, and converts it to
// pixels per minute. ok, when non-nil, marks pixels whose flow was solved.
func toField(f *flowField, prev, cur *image, ok []bool, blockSize int, minutes float64) *motion.Field {
	bw, bh := f.w/blockSize, f.h/blockSize
	v := make([]motion.Vector, bw*bh)
	valid := make([]bool, bw*bh)
	need := max(1, blockSize*blockSize/20)
	for by := range bh {
		for bx := range bw {
			var su, sv float64
			n := 0
			for y := by * blockSize; y < (by+1)*blockSize; y++ {
				for x := bx * blockSize; x < (bx+1)*blockSize; x++ {
					i := y*f.w + x
					if (prev.p[i] <= 0 && cur.p[i] <= 0) || (ok != nil && !ok[i]) {
						continue
					}
					su += float64(f.u[i])
					sv += float64(f.v[i])
					n++
				}
			}
			if n >= need {
				j := by*bw + bx
				v[j] = motion.Vector{DX: su / float64(n) / minutes, DY: sv / float64(n) / minutes}
				valid[j] = true
			}
		}
	}
	return motion.FromBlocks(blockSize, bw, bh, v, valid)
}
