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

// Bilinear interpolates the echo at (x, y) between the four nearest pixel
// centers, which lie on integer coordinates.
func (g *Grid) Bilinear(x, y float64) float32 {
	x0, y0 := math.Floor(x), math.Floor(y)
	fx, fy := float32(x-x0), float32(y-y0)
	ix, iy := int(x0), int(y0)
	top := g.At(ix, iy)*(1-fx) + g.At(ix+1, iy)*fx
	bottom := g.At(ix, iy+1)*(1-fx) + g.At(ix+1, iy+1)*fx
	return top*(1-fy) + bottom*fy
}

// PointEcho is the echo used for the point (x, y): the echo at the point
// itself when it reaches strong dBZ, else the median within r pixels.
// Convective cores are often only a pixel or two across, and the median
// alone drops them for the drier ring around, though the map shows the
// core over the point; weak echoes keep the median, which ignores specks.
func (g *Grid) PointEcho(x, y float64, r int, strong float32) float32 {
	if strong > 0 {
		if v := g.Bilinear(x, y); v >= strong {
			return v
		}
	}
	return g.MedianInRadius(x, y, r)
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
