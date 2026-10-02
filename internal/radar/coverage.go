package radar

import (
	"bytes"
	"fmt"
	"image/color"
	"image/png"
)

// Coverage marks where radar data exists. RainViewer coverage tiles are
// transparent where a radar sees and opaque black where none does; without
// it, an uncovered area looks exactly like clear sky.
type Coverage struct {
	W, H    int
	Covered []bool
}

// DecodeCoverage reads a RainViewer coverage tile.
func DecodeCoverage(data []byte) (*Coverage, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("radar: decode coverage: %w", err)
	}
	b := img.Bounds()
	c := &Coverage{W: b.Dx(), H: b.Dy(), Covered: make([]bool, b.Dx()*b.Dy())}
	for y := 0; y < c.H; y++ {
		for x := 0; x < c.W; x++ {
			a := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA).A
			c.Covered[y*c.W+x] = a == 0
		}
	}
	return c, nil
}

// BuildCoverageMosaic stitches an n×n block of coverage tiles whose top-left
// is (x0, y0), matching BuildMosaic. Missing tiles count as not covered.
func BuildCoverageMosaic(tiles map[TileKey][]byte, x0, y0, n int) (*Coverage, error) {
	m := &Coverage{W: n * tileSize, H: n * tileSize, Covered: make([]bool, n*n*tileSize*tileSize)}
	for ty := 0; ty < n; ty++ {
		for tx := 0; tx < n; tx++ {
			data, ok := tiles[TileKey{x0 + tx, y0 + ty}]
			if !ok {
				continue
			}
			t, err := DecodeCoverage(data)
			if err != nil {
				return nil, fmt.Errorf("coverage %d/%d: %w", x0+tx, y0+ty, err)
			}
			if t.W != tileSize || t.H != tileSize {
				return nil, fmt.Errorf("coverage %d/%d: size %dx%d", x0+tx, y0+ty, t.W, t.H)
			}
			for y := 0; y < tileSize; y++ {
				copy(m.Covered[(ty*tileSize+y)*m.W+tx*tileSize:], t.Covered[y*tileSize:(y+1)*tileSize])
			}
		}
	}
	return m, nil
}

// At reports whether (x, y) is covered; outside the mask is not.
func (c *Coverage) At(x, y int) bool {
	if c == nil {
		return true // no mask: assume everything is seen
	}
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return false
	}
	return c.Covered[y*c.W+x]
}

// Fraction is the covered share of the w×h rectangle at (x0, y0).
func (c *Coverage) Fraction(x0, y0, w, h int) float64 {
	n := 0
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			if c.At(x, y) {
				n++
			}
		}
	}
	return float64(n) / float64(w*h)
}

// RainFraction is the share of covered pixels in the w×h rectangle at
// (x0, y0) with at least dbz; cov may be nil. It returns 0 when nothing in
// the rectangle is covered.
func RainFraction(g *Grid, cov *Coverage, x0, y0, w, h int, dbz float32) float64 {
	var covered, rain int
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			if !cov.At(x, y) {
				continue
			}
			covered++
			if g.At(x, y) >= dbz {
				rain++
			}
		}
	}
	if covered == 0 {
		return 0
	}
	return float64(rain) / float64(covered)
}
