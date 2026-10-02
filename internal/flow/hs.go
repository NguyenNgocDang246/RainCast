package flow

import (
	"runtime"
	"sync"

	"raincast/internal/motion"
	"raincast/internal/radar"
)

// HSOptions tunes Horn–Schunck.
type HSOptions struct {
	Levels int     // pyramid levels; 4 levels see a 16 px move as 2 px
	Alpha  float64 // smoothness weight; larger gives smoother flow
	// Iters is the relaxation steps per warp at the coarsest level; each
	// finer level, which starts from the coarser answer, gets half, down to
	// MinIters.
	Iters, MinIters int
	Warps           int // re-linearizations per level
	BlockSize       int // output field block size in pixels
	Workers         int // goroutines per relaxation step; 0 means GOMAXPROCS
}

// DefaultHS suits 10-minute frames at ~1.2 km per pixel.
func DefaultHS() HSOptions {
	return HSOptions{Levels: 4, Alpha: 10, Iters: 80, MinIters: 10, Warps: 2, BlockSize: 16}
}

// HornSchunck estimates motion prev → cur, minutes apart, as a field in
// pixels per minute. The flow is solved for every pixel under brightness
// constancy plus a global smoothness penalty, coarse to fine.
func HornSchunck(prev, cur *radar.Grid, minutes float64, opt HSOptions) *motion.Field {
	p0, c0 := fromGrid(prev), fromGrid(cur)
	pp, cp := pyramid(p0, opt.Levels), pyramid(c0, opt.Levels)
	a2 := float32(opt.Alpha * opt.Alpha)
	workers := opt.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	var f *flowField
	iters := opt.Iters
	for l := len(pp) - 1; l >= 0; l-- {
		pl, cl := pp[l], cp[l]
		if f == nil {
			f = newFlow(pl.w, pl.h)
		} else {
			f = f.upsample(pl.w, pl.h)
		}
		for range opt.Warps {
			ix, iy, it := gradients(pl, warp(cl, f))
			hsRelax(f, ix, iy, it, a2, iters, workers)
		}
		iters = max(opt.MinIters, iters/2)
	}
	return toField(f, p0, c0, nil, opt.BlockSize, minutes)
}

// hsRelax runs Jacobi steps of Horn–Schunck on the linearization around the
// current flow (u0, v0), Ix·(u−u0) + Iy·(v−v0) + It = 0, smoothing the
// total flow. Rows are split between workers.
func hsRelax(f *flowField, ix, iy, it []float32, a2 float32, iters, workers int) {
	w, h := f.w, f.h
	// With c = Ix·u0 + Iy·v0 − It, the update is
	// u = ū − Ix·(Ix·ū + Iy·v̄ − c) / (α² + Ix² + Iy²).
	c := make([]float32, len(f.u))
	den := make([]float32, len(f.u))
	for i := range c {
		c[i] = ix[i]*f.u[i] + iy[i]*f.v[i] - it[i]
		den[i] = 1 / (a2 + ix[i]*ix[i] + iy[i]*iy[i])
	}
	nu, nv := make([]float32, len(f.u)), make([]float32, len(f.v))
	rows := func(y0, y1 int) {
		u, v := f.u, f.v
		for y := y0; y < y1; y++ {
			ya, yb := max(y-1, 0)*w, min(y+1, h-1)*w
			row := y * w
			for x := range w {
				xa, xb := max(x-1, 0), min(x+1, w-1)
				ub := (u[row+xa]+u[row+xb]+u[ya+x]+u[yb+x])/6 + (u[ya+xa]+u[ya+xb]+u[yb+xa]+u[yb+xb])/12
				vb := (v[row+xa]+v[row+xb]+v[ya+x]+v[yb+x])/6 + (v[ya+xa]+v[ya+xb]+v[yb+xa]+v[yb+xb])/12
				i := row + x
				t := (ix[i]*ub + iy[i]*vb - c[i]) * den[i]
				nu[i] = ub - ix[i]*t
				nv[i] = vb - iy[i]*t
			}
		}
	}
	chunk := (h + workers - 1) / workers
	for range iters {
		if workers == 1 || h < 64 {
			rows(0, h)
		} else {
			var wg sync.WaitGroup
			for y0 := 0; y0 < h; y0 += chunk {
				wg.Add(1)
				go func() {
					defer wg.Done()
					rows(y0, min(y0+chunk, h))
				}()
			}
			wg.Wait()
		}
		f.u, nu = nu, f.u
		f.v, nv = nv, f.v
	}
}
