package motion

import (
	"math"

	"raincast/internal/radar"
)

// Trend is the per-block rate at which echoes strengthen or weaken, in
// dBZ per minute. It is measured along the motion (Lagrangian): each pixel
// is compared with where its rain was in the earlier frame, so a moving cell
// is not mistaken for one that grows.
type Trend struct {
	BlockSize int
	BW, BH    int
	R         []float64
}

const (
	trendFloor   = 10  // dBZ; weaker echoes count as this so new cells show growth
	trendMaxRate = 1.0 // dBZ/min; radar noise beyond this is not physical
	trendMinFrac = 0.05
)

// EstimateTrend compares cur with prev, which is `minutes` older, following
// motion field f. Blocks with too little rain get no trend.
func EstimateTrend(prev, cur *radar.Grid, f *Field, minutes float64, rainDBZ float32) *Trend {
	bs := f.BlockSize
	t := &Trend{BlockSize: bs, BW: f.BW, BH: f.BH, R: make([]float64, f.BW*f.BH)}
	raw := make([]float64, len(t.R))
	ok := make([]bool, len(t.R))
	clampI := func(v float32) float64 { return math.Max(float64(v), trendFloor) }

	for by := 0; by < f.BH; by++ {
		for bx := 0; bx < f.BW; bx++ {
			var sum float64
			var n int
			for y := by * bs; y < (by+1)*bs; y++ {
				for x := bx * bs; x < (bx+1)*bs; x++ {
					v := f.At(float64(x), float64(y))
					px := int(math.Round(float64(x) - v.DX*minutes))
					py := int(math.Round(float64(y) - v.DY*minutes))
					now, was := cur.At(x, y), prev.At(px, py)
					if now < rainDBZ && was < rainDBZ {
						continue
					}
					sum += clampI(now) - clampI(was)
					n++
				}
			}
			i := by*f.BW + bx
			if float64(n) >= trendMinFrac*float64(bs*bs) {
				raw[i], ok[i] = sum/float64(n)/minutes, true
			}
		}
	}

	// 3×3 mean over blocks that had enough rain smooths radar noise.
	for by := 0; by < f.BH; by++ {
		for bx := 0; bx < f.BW; bx++ {
			var sum float64
			var n int
			for ny := by - 1; ny <= by+1; ny++ {
				for nx := bx - 1; nx <= bx+1; nx++ {
					if nx >= 0 && ny >= 0 && nx < f.BW && ny < f.BH && ok[ny*f.BW+nx] {
						sum += raw[ny*f.BW+nx]
						n++
					}
				}
			}
			if n > 0 {
				t.R[by*f.BW+bx] = math.Max(-trendMaxRate, math.Min(trendMaxRate, sum/float64(n)))
			}
		}
	}
	return t
}

// At returns the trend at pixel (x, y), bilinearly interpolated.
func (t *Trend) At(x, y float64) float64 {
	if t == nil || t.BW == 0 || t.BH == 0 {
		return 0
	}
	bs := float64(t.BlockSize)
	fx := clamp(x/bs-0.5, 0, float64(t.BW-1))
	fy := clamp(y/bs-0.5, 0, float64(t.BH-1))
	x0, y0 := int(fx), int(fy)
	x1, y1 := min(x0+1, t.BW-1), min(y0+1, t.BH-1)
	tx, ty := fx-float64(x0), fy-float64(y0)
	r := func(x, y int) float64 { return t.R[y*t.BW+x] }
	return (r(x0, y0)*(1-tx)+r(x1, y0)*tx)*(1-ty) + (r(x0, y1)*(1-tx)+r(x1, y1)*tx)*ty
}
