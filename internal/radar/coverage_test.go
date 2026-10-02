package radar

import (
	"os"
	"testing"
)

// The fixtures are real RainViewer coverage tiles: 7/101/60 is Ho Chi Minh
// City (radar), 7/50/50 the middle of the Atlantic (none).
func TestDecodeCoverage(t *testing.T) {
	for _, c := range []struct {
		file     string
		min, max float64
	}{
		{"testdata/coverage_7_101_60.png", 0.99, 1},
		{"testdata/coverage_7_50_50.png", 0, 0},
	} {
		data, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		cov, err := DecodeCoverage(data)
		if err != nil {
			t.Fatal(err)
		}
		if f := cov.Fraction(0, 0, 256, 256); f < c.min || f > c.max {
			t.Errorf("%s: covered %.4f, want [%v, %v]", c.file, f, c.min, c.max)
		}
	}
}

func TestCoverageMosaicAndRainFraction(t *testing.T) {
	land, _ := os.ReadFile("testdata/coverage_7_101_60.png")
	sea, _ := os.ReadFile("testdata/coverage_7_50_50.png")
	// Left column covered, the rest not; the bottom-right tile is missing.
	tiles := map[TileKey][]byte{}
	for y := range 3 {
		for x := range 3 {
			switch {
			case x == 0:
				tiles[TileKey{10 + x, 20 + y}] = land
			case x != 2 || y != 2:
				tiles[TileKey{10 + x, 20 + y}] = sea
			}
		}
	}
	cov, err := BuildCoverageMosaic(tiles, 10, 20, 3)
	if err != nil {
		t.Fatal(err)
	}
	if f := cov.Fraction(0, 0, 768, 768); f < 0.33 || f > 0.334 {
		t.Errorf("mosaic covered %.4f, want ≈ 1/3", f)
	}
	if !cov.At(10, 10) || cov.At(300, 10) || cov.At(700, 700) || cov.At(-1, 0) {
		t.Error("At disagrees with the tiles")
	}

	g := NewGrid(768, 768)
	for y := range 768 {
		for x := range 128 {
			g.Set(x, y, 35) // rain on half of the covered column
		}
		g.Set(400, y, 50) // rain where there is no radar does not count
	}
	if f := RainFraction(g, cov, 0, 0, 768, 768, 20); f < 0.49 || f > 0.51 {
		t.Errorf("rain fraction %.3f, want ≈ 0.5 of covered pixels", f)
	}
	if f := RainFraction(g, nil, 0, 0, 256, 256, 20); f != 0.5 {
		t.Errorf("without a mask: %.3f, want 0.5", f)
	}
	if f := RainFraction(g, cov, 256, 0, 256, 256, 20); f != 0 {
		t.Errorf("uncovered area: %.3f, want 0", f)
	}
}
