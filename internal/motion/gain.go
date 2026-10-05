package motion

import (
	"math"

	"raincast/internal/radar"
)

// gainMinRain is the share of rainy pixels (in either frame) a window needs
// for TranslationGain to judge it.
const gainMinRain = 0.02

// TranslationGain measures how well moving the rain by v explains the
// change from prev to cur, which is `minutes` older, within r pixels of
// (x, y): 1 − (error of prev shifted by v) / (error of prev left still).
// Near 1, v is how the rain moved; 0 or below, v explains the change no
// better than standing still, as when cells grow or spread in place. ok is
// false when the window has too little rain or did not change.
func TranslationGain(prev, cur *radar.Grid, v Vector, minutes, x, y float64, r int) (gain float64, ok bool) {
	sx, sy := v.DX*minutes, v.DY*minutes
	cx, cy := int(math.Round(x)), int(math.Round(y))
	var still, moved float64
	n, rainy := 0, 0
	for py := cy - r; py <= cy+r; py++ {
		for px := cx - r; px <= cx+r; px++ {
			if px < 0 || py < 0 || px >= cur.W || py >= cur.H || (px-cx)*(px-cx)+(py-cy)*(py-cy) > r*r {
				continue
			}
			c := intensity(cur.At(px, py))
			p := intensity(prev.At(px, py))
			s := bilinear(prev, float64(px)-sx, float64(py)-sy)
			still += (c - p) * (c - p)
			moved += (c - s) * (c - s)
			n++
			if c > 0 || p > 0 {
				rainy++
			}
		}
	}
	// A change under 1 dB per pixel on average is noise, not motion.
	if n == 0 || float64(rainy) < gainMinRain*float64(n) || still < float64(n) {
		return 0, false
	}
	return 1 - moved/still, true
}

// bilinear is the matching intensity of g at (x, y), interpolated.
func bilinear(g *radar.Grid, x, y float64) float64 {
	x0, y0 := math.Floor(x), math.Floor(y)
	tx, ty := x-x0, y-y0
	ix, iy := int(x0), int(y0)
	at := func(dx, dy int) float64 { return intensity(g.At(ix+dx, iy+dy)) }
	return (at(0, 0)*(1-tx)+at(1, 0)*tx)*(1-ty) + (at(0, 1)*(1-tx)+at(1, 1)*tx)*ty
}
