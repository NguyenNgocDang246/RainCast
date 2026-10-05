package motion

import (
	"math"
	"testing"

	"raincast/internal/radar"
)

// disc draws a soft cell of radius r at (cx, cy).
func disc(g *radar.Grid, cx, cy, r float64) {
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy) / r
			if d <= 1 && float32(50-25*d) > g.At(x, y) {
				g.Set(x, y, float32(50-25*d))
			}
		}
	}
}

func TestTranslationGainMovingCell(t *testing.T) {
	prev, cur := radar.NewGrid(128, 128), radar.NewGrid(128, 128)
	disc(prev, 60, 64, 12)
	disc(cur, 65, 64, 12) // 5 px east in 10 minutes
	if g, ok := TranslationGain(prev, cur, Vector{DX: 0.5}, 10, 64, 64, 24); !ok || g < 0.5 {
		t.Errorf("true motion: gain %.2f ok=%v, want > 0.5", g, ok)
	}
	if g, ok := TranslationGain(prev, cur, Vector{}, 10, 64, 64, 24); !ok || g > 1e-9 {
		t.Errorf("no motion: gain %.2f ok=%v, want 0", g, ok)
	}
	if g, ok := TranslationGain(prev, cur, Vector{DX: -0.5}, 10, 64, 64, 24); !ok || g > 0 {
		t.Errorf("backward motion: gain %.2f ok=%v, want <= 0", g, ok)
	}
}

// A cell that stays put while new rain forms on its southwest side looks
// like motion southwest, but shifting it there explains little.
func TestTranslationGainSpreadingCell(t *testing.T) {
	prev, cur := radar.NewGrid(128, 128), radar.NewGrid(128, 128)
	disc(prev, 64, 64, 12)
	disc(cur, 64, 64, 14)
	disc(cur, 56, 72, 10)
	sw := Vector{DX: -0.3, DY: 0.3}
	if g, ok := TranslationGain(prev, cur, sw, 10, 64, 64, 24); !ok || g > 0.2 {
		t.Errorf("spreading: gain %.2f ok=%v, want < 0.2", g, ok)
	}
}

func TestTranslationGainNoRain(t *testing.T) {
	g := radar.NewGrid(64, 64)
	if _, ok := TranslationGain(g, g, Vector{DX: 0.5}, 10, 32, 32, 24); ok {
		t.Error("empty window should not be judged")
	}
}
