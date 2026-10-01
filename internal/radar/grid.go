package radar

import (
	"math"
	"slices"
)

// Grid is a rectangular field of reflectivity values in dBZ.
type Grid struct {
	W, H int
	Data []float32
}

// NewGrid returns a grid filled with MinDBZ.
func NewGrid(w, h int) *Grid {
	g := &Grid{W: w, H: h, Data: make([]float32, w*h)}
	for i := range g.Data {
		g.Data[i] = MinDBZ
	}
	return g
}

// At returns the value at (x, y), or MinDBZ outside the grid.
func (g *Grid) At(x, y int) float32 {
	if x < 0 || y < 0 || x >= g.W || y >= g.H {
		return MinDBZ
	}
	return g.Data[y*g.W+x]
}

// Set writes v at (x, y); out-of-range writes are ignored.
func (g *Grid) Set(x, y int, v float32) {
	if x < 0 || y < 0 || x >= g.W || y >= g.H {
		return
	}
	g.Data[y*g.W+x] = v
}

// MedianInRadius returns the median echo within r pixels of (x, y). It
// reports rain only when most of the neighborhood is rainy, so a cell edge a
// few kilometers away does not count as rain at the point.
func (g *Grid) MedianInRadius(x, y float64, r int) float32 {
	cx, cy := int(math.Round(x)), int(math.Round(y))
	vals := make([]float32, 0, (2*r+1)*(2*r+1))
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy <= r*r {
				vals = append(vals, g.At(cx+dx, cy+dy))
			}
		}
	}
	slices.Sort(vals)
	return vals[len(vals)/2]
}

// Mosaic is a Grid stitched from adjacent tiles at one zoom level.
type Mosaic struct {
	*Grid
	Zoom int
	// TileX, TileY are the tile coordinates of the top-left tile.
	TileX, TileY int
}

// Local converts global pixel coordinates to mosaic-local ones.
func (m *Mosaic) Local(gx, gy float64) (x, y float64) {
	return gx - float64(m.TileX*tileSize), gy - float64(m.TileY*tileSize)
}
