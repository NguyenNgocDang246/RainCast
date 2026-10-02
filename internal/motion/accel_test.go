package motion

import (
	"math"
	"testing"
)

func uniform(bw, bh int, v Vector) *Field {
	vs := make([]Vector, bw*bh)
	valid := make([]bool, bw*bh)
	for i := range vs {
		vs[i], valid[i] = v, true
	}
	return FromBlocks(32, bw, bh, vs, valid)
}

// A cell speeding up from 0.3 to 0.4 px/min east over 20 minutes
// accelerates at 0.005 px/min².
func TestAccelOfTwoTranslations(t *testing.T) {
	a := Accel(uniform(8, 8, Vector{DX: 0.4}), uniform(8, 8, Vector{DX: 0.3}), 20)
	if v := a.At(100, 100); math.Abs(v.DX-0.005) > 1e-9 || math.Abs(v.DY) > 1e-9 {
		t.Fatalf("accel = %+v, want (0.005, 0)", v)
	}
}

// A jump no echo makes (a bad match) is bounded.
func TestAccelIsBounded(t *testing.T) {
	a := Accel(uniform(4, 4, Vector{DX: 2}), uniform(4, 4, Vector{}), 10)
	if n := math.Hypot(a.At(64, 64).DX, a.At(64, 64).DY); math.Abs(n-accelMax) > 1e-9 {
		t.Fatalf("|accel| = %.4f, want the bound %.4f", n, accelMax)
	}
}
