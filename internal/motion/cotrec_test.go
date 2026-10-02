package motion

import (
	"math"
	"testing"
)

func field(w, h int, v func(x, y int) Vector) *Field {
	f := &Field{BlockSize: 32, BW: w, BH: h, V: make([]Vector, w*h), Valid: make([]bool, w*h)}
	for y := range h {
		for x := range w {
			f.V[y*w+x] = v(x, y)
			f.Valid[y*w+x] = true
		}
	}
	f.NValid = w * h
	return f
}

func TestCotrecKeepsTranslationAndRotation(t *testing.T) {
	for name, v := range map[string]func(x, y int) Vector{
		"uniform":  func(x, y int) Vector { return Vector{0.8, -0.3} },
		"rotation": func(x, y int) Vector { return Vector{-0.05 * float64(y-12), 0.05 * float64(x-12)} },
	} {
		f := field(24, 24, v)
		c := f.Cotrec(200)
		for i := range f.V {
			if math.Abs(c.V[i].DX-f.V[i].DX) > 1e-6 || math.Abs(c.V[i].DY-f.V[i].DY) > 1e-6 {
				t.Fatalf("%s: block %d changed %v → %v", name, i, f.V[i], c.V[i])
			}
		}
	}
}

func TestCotrecRemovesDivergence(t *testing.T) {
	// Steady eastward motion plus a spurious source in the middle, as block
	// matching produces around a growing cell.
	f := field(24, 24, func(x, y int) Vector {
		dx, dy := float64(x-12), float64(y-12)
		g := 0.4 * math.Exp(-(dx*dx+dy*dy)/18)
		return Vector{0.6 + g*dx/3, g * dy / 3}
	})
	c := f.Cotrec(400)
	if before, after := f.Divergence(), c.Divergence(); after > before/4 {
		t.Fatalf("divergence %.4f → %.4f, want at least 4× smaller", before, after)
	}
	if math.Abs(c.Global.DX-0.6) > 0.05 || math.Abs(c.Global.DY) > 0.05 {
		t.Fatalf("global motion changed to %+v", c.Global)
	}
}
