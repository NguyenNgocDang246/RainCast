package motion

import "math"

// accelMax bounds acceleration in pixels per minute²: 0.02 lets a cell
// gain or lose ~0.2 px/min (~15 km/h) over ten minutes, more than radar
// echoes change speed in practice.
const accelMax = 0.02

// Accel is how motion changed from older to newer, minutes apart, in
// pixels per minute²: per block where both fields were measured, then
// smoothed and filled like a motion field, and bounded by accelMax.
func Accel(newer, older *Field, minutes float64) *Field {
	if newer == nil || older == nil || minutes <= 0 || len(newer.V) != len(older.V) {
		return nil
	}
	v := make([]Vector, len(newer.V))
	valid := make([]bool, len(newer.V))
	for i := range v {
		if newer.Valid[i] && older.Valid[i] {
			v[i] = Vector{(newer.V[i].DX - older.V[i].DX) / minutes, (newer.V[i].DY - older.V[i].DY) / minutes}
			valid[i] = true
		}
	}
	a := FromBlocks(newer.BlockSize, newer.BW, newer.BH, v, valid)
	for i, x := range a.V {
		if n := math.Hypot(x.DX, x.DY); n > accelMax {
			a.V[i] = Vector{x.DX * accelMax / n, x.DY * accelMax / n}
		}
	}
	if n := math.Hypot(a.Global.DX, a.Global.DY); n > accelMax {
		a.Global = Vector{a.Global.DX * accelMax / n, a.Global.DY * accelMax / n}
	}
	return a
}
