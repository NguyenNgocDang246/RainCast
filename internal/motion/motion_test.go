package motion

import (
	"math"
	"testing"

	"raincast/internal/radar"
)

// blobs draws a few soft rain cells shifted by (sx, sy).
func blobs(w, h int, sx, sy float64) *radar.Grid {
	g := radar.NewGrid(w, h)
	cells := [][3]float64{{80, 90, 18}, {150, 70, 12}, {110, 160, 22}, {170, 170, 10}}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var v float64 = float64(radar.MinDBZ)
			for _, c := range cells {
				dx, dy := float64(x)-(c[0]+sx), float64(y)-(c[1]+sy)
				d := math.Sqrt(dx*dx+dy*dy) / c[2]
				if d < 1.5 {
					v = math.Max(v, 50-20*d)
				}
			}
			g.Set(x, y, float32(v))
		}
	}
	return g
}

func TestEstimateRecoversShift(t *testing.T) {
	prev := blobs(256, 256, 0, 0)
	cur := blobs(256, 256, 5, -3)
	f := Estimate(prev, cur, 10, DefaultOptions())
	if !f.Reliable() {
		t.Fatal("no block matched")
	}
	// Vectors are per minute; the frames are 10 minutes apart.
	if math.Abs(f.Global.DX*10-5) > 1 || math.Abs(f.Global.DY*10+3) > 1 {
		t.Fatalf("global = %+v px/min, want (0.5,-0.3)", f.Global)
	}
	v := f.At(110, 160)
	if math.Abs(v.DX*10-5) > 1 || math.Abs(v.DY*10+3) > 1 {
		t.Fatalf("At(cell) = %+v", v)
	}
}

func TestEstimateNoRain(t *testing.T) {
	g := radar.NewGrid(128, 128)
	f := Estimate(g, g, 10, DefaultOptions())
	if f.Reliable() {
		t.Fatal("empty grids should not be reliable")
	}
	if v := f.At(10, 10); v != (Vector{}) {
		t.Fatalf("At = %+v, want zero", v)
	}
}

func TestAverage(t *testing.T) {
	a := blobs(256, 256, 0, 0)
	b := blobs(256, 256, 4, 2)
	c := blobs(256, 256, 8, 4)
	f := Average(Estimate(a, b, 10, DefaultOptions()), Estimate(b, c, 10, DefaultOptions()))
	if math.Abs(f.Global.DX*10-4) > 1 || math.Abs(f.Global.DY*10-2) > 1 {
		t.Fatalf("global = %+v", f.Global)
	}
}

func TestWeightedAverage(t *testing.T) {
	mk := func(dx float64) *Field {
		f := &Field{BlockSize: 32, BW: 1, BH: 1, V: []Vector{{DX: dx}}, Valid: []bool{true}}
		f.finish()
		return f
	}
	f := WeightedAverage([]*Field{mk(1), mk(-1), mk(4)}, []float64{3, 2, 0})
	if math.Abs(f.V[0].DX-0.2) > 1e-9 {
		t.Fatalf("dx = %v, want (3-2)/5 = 0.2", f.V[0].DX)
	}
}

// A cell that moves and strengthens shows growth; one that only moves does not.
func TestEstimateTrend(t *testing.T) {
	prev := blobs(256, 256, 0, 0)
	moved := blobs(256, 256, 5, 0)
	f := Estimate(prev, moved, 10, DefaultOptions())
	if tr := EstimateTrend(prev, moved, f, 10, 15); math.Abs(tr.At(110, 160)) > 0.15 {
		t.Fatalf("pure motion trend = %.3f dBZ/min, want ~0", tr.At(110, 160))
	}

	grown := radar.NewGrid(256, 256)
	for i, v := range moved.Data {
		if v >= 10 {
			v += 6 // +6 dBZ in 10 minutes
		}
		grown.Data[i] = v
	}
	tr := EstimateTrend(prev, grown, Estimate(prev, grown, 10, DefaultOptions()), 10, 15)
	if got := tr.At(110, 160); got < 0.3 || got > 0.7 {
		t.Fatalf("growth trend = %.3f dBZ/min, want ~0.6", got)
	}
}
