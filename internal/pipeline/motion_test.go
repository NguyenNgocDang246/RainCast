package pipeline

import (
	"math"
	"testing"
	"time"

	"raincast/internal/radar"
	"raincast/internal/rainviewer"
)

// cellsAt draws a few rain cells shifted dx pixels east.
func cellsAt(dx int) *radar.Mosaic {
	g := radar.NewGrid(256, 256)
	for _, c := range [][3]int{{70, 90, 16}, {150, 70, 12}, {110, 170, 20}} {
		for y := c[1] - c[2]; y <= c[1]+c[2]; y++ {
			for x := c[0] - c[2]; x <= c[0]+c[2]; x++ {
				d := math.Hypot(float64(x-c[0]), float64(y-c[1])) / float64(c[2])
				if d <= 1 {
					g.Set(x+dx, y, float32(50-25*d))
				}
			}
		}
	}
	return &radar.Mosaic{Grid: g}
}

// globalDX runs estimateMotion over frames 10 min apart with the given
// eastward positions and returns the motion in pixels per 10 minutes.
func globalDX(t *testing.T, positions []int, pairs int) float64 {
	t.Helper()
	grids := map[int64]*radar.Mosaic{}
	var frames []rainviewer.Frame
	for i, x := range positions {
		ts := int64(600 * (i + 1))
		frames = append(frames, rainviewer.Frame{Time: ts})
		grids[ts] = cellsAt(x)
	}
	f, used := estimateMotion(frames, func(ts int64) *radar.Mosaic { return grids[ts] }, 30*time.Minute, pairs, nil)
	if f == nil || used != min(pairs, len(positions)-1)+1 {
		t.Fatalf("field=%v used=%d", f, used)
	}
	return f.Global.DX * 10
}

func TestMotionSteadyDrift(t *testing.T) {
	if dx := globalDX(t, []int{0, 4, 8, 12, 16}, 4); math.Abs(dx-4) > 1 {
		t.Fatalf("steady drift = %.2f px/10min, want 4", dx)
	}
}

// Rain flickering between two spots must not be extrapolated as a drift.
func TestMotionOscillationCancels(t *testing.T) {
	// A → B → A → B → A: the newest pair alone says "moving west 6 px".
	if dx := globalDX(t, []int{0, 6, 0, 6, 0}, 1); math.Abs(dx+6) > 1 {
		t.Fatalf("single pair = %.2f, want -6", dx)
	}
	if dx := globalDX(t, []int{0, 6, 0, 6, 0}, 4); math.Abs(dx) > 1.5 {
		t.Fatalf("4 pairs = %.2f px/10min, want ~0", dx)
	}
}
