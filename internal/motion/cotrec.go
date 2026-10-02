package motion

import "math"

// Cotrec returns f with its divergence removed (COTREC: TREC vectors
// constrained by continuity). Block matching often yields neighboring
// vectors that converge or spread out, which advection turns into rain
// piling up or tearing apart; real echo motion is close to non-divergent.
// The divergent part is the gradient of a potential φ with ∇²φ = ∇·V,
// solved by Jacobi iteration on the block grid; subtracting ∇φ keeps the
// rotational and uniform motion untouched.
func (f *Field) Cotrec(iters int) *Field {
	w, h := f.BW, f.BH
	out := &Field{BlockSize: f.BlockSize, BW: w, BH: h, V: append([]Vector(nil), f.V...),
		Valid: append([]bool(nil), f.Valid...), Global: f.Global, NValid: f.NValid}
	if w < 3 || h < 3 || f.NValid == 0 {
		return out
	}
	// Neumann boundaries: indices clamp, so the edge has no flux through it.
	at := func(x, y int) Vector {
		return f.V[min(max(y, 0), h-1)*w+min(max(x, 0), w-1)]
	}
	div := make([]float64, w*h)
	for y := range h {
		for x := range w {
			div[y*w+x] = (at(x+1, y).DX-at(x-1, y).DX)/2 + (at(x, y+1).DY-at(x, y-1).DY)/2
		}
	}
	phi := make([]float64, w*h)
	next := make([]float64, w*h)
	p := func(x, y int) float64 { return phi[min(max(y, 0), h-1)*w+min(max(x, 0), w-1)] }
	for range iters {
		for y := range h {
			for x := range w {
				next[y*w+x] = (p(x-1, y) + p(x+1, y) + p(x, y-1) + p(x, y+1) - div[y*w+x]) / 4
			}
		}
		phi, next = next, phi
	}
	for y := range h {
		for x := range w {
			i := y*w + x
			out.V[i].DX -= (p(x+1, y) - p(x-1, y)) / 2
			out.V[i].DY -= (p(x, y+1) - p(x, y-1)) / 2
		}
	}
	xs := make([]float64, 0, len(out.V))
	ys := make([]float64, 0, len(out.V))
	for i, ok := range out.Valid {
		if ok {
			xs = append(xs, out.V[i].DX)
			ys = append(ys, out.V[i].DY)
		}
	}
	if len(xs) > 0 {
		out.Global = Vector{median(xs), median(ys)}
	}
	return out
}

// Divergence is the mean absolute divergence of f per block, for tests and
// diagnostics.
func (f *Field) Divergence() float64 {
	w, h := f.BW, f.BH
	if w < 3 || h < 3 {
		return 0
	}
	var sum float64
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			d := (f.V[y*w+x+1].DX-f.V[y*w+x-1].DX)/2 + (f.V[(y+1)*w+x].DY-f.V[(y-1)*w+x].DY)/2
			sum += math.Abs(d)
		}
	}
	return sum / float64((w-2)*(h-2))
}
