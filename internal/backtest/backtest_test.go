package backtest

import (
	"math"
	"testing"

	"raincast/internal/radar"
)

// moving draws one rain band that moves `speed` px east per frame.
func moving(frameIdx int, speed int) *radar.Mosaic {
	g := radar.NewGrid(192, 192)
	cx := 40 + frameIdx*speed
	for y := 0; y < 192; y++ {
		for x := 0; x < 192; x++ {
			d := math.Hypot(float64(x-cx), float64(y-96))
			if d < 25 {
				g.Set(x, y, float32(45-15*d/25))
			}
		}
	}
	return &radar.Mosaic{Grid: g}
}

func TestMotionBeatsPersistenceOnMovingRain(t *testing.T) {
	var frames []Frame
	for i := range 12 {
		frames = append(frames, Frame{Time: int64(600 * (i + 1)), Mosaic: moving(i, 6)})
	}
	rep := Run(frames, Config{
		Variants:  []Variant{{Name: "m2", Pairs: 2}},
		Leads:     []int{10, 20, 30},
		Threshold: 20, Radius: 1, Step: 4, StepSec: 600,
	})
	if rep.Issues == 0 || len(rep.Results) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	persist, model := rep.Results[0].Overall, rep.Results[1].Overall
	if model.CSI == nil || persist.CSI == nil || *model.CSI <= *persist.CSI {
		t.Fatalf("model CSI %v should beat persistence %v", model.CSI, persist.CSI)
	}
	if *model.CSI < 0.8 {
		t.Fatalf("model CSI = %.2f on perfectly steady motion", *model.CSI)
	}
}
