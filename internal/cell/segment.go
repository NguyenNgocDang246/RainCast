// Package cell tracks rain cells as objects (TITAN/SCIT style): it splits
// each radar frame into cells and follows them between frames, one to one
// (Hungarian assignment with a Kalman-smoothed velocity) or by overlap,
// which also sees cells form, split, merge and decay. A storm's life then
// adjusts the intensity trend of whichever motion method moved it.
package cell

import "raincast/internal/radar"

// Cell is one connected area at or above the threshold.
type Cell struct {
	X, Y                   float64 // intensity-weighted centroid, pixels
	Area                   int
	Peak                   float32
	MinX, MinY, MaxX, MaxY int     // bounding box, inclusive
	Pixels                 []int32 // indices into the grid
}

// Segment finds the 4-connected areas of g at or above thr with at least
// minArea pixels.
func Segment(g *radar.Grid, thr float32, minArea int) []Cell {
	seen := make([]bool, len(g.Data))
	var cells []Cell
	stack := make([]int32, 0, 256)
	for start, v := range g.Data {
		if seen[start] || v < thr {
			continue
		}
		c := Cell{MinX: g.W, MinY: g.H, MaxX: -1, MaxY: -1}
		var wsum float64
		stack = append(stack[:0], int32(start))
		seen[start] = true
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := int(i)%g.W, int(i)/g.W
			val := g.Data[i]
			w := float64(val - thr + 1)
			c.X += w * float64(x)
			c.Y += w * float64(y)
			wsum += w
			c.Peak = max(c.Peak, val)
			c.MinX, c.MaxX = min(c.MinX, x), max(c.MaxX, x)
			c.MinY, c.MaxY = min(c.MinY, y), max(c.MaxY, y)
			c.Pixels = append(c.Pixels, i)
			for _, n := range [4][2]int{{x - 1, y}, {x + 1, y}, {x, y - 1}, {x, y + 1}} {
				if n[0] < 0 || n[1] < 0 || n[0] >= g.W || n[1] >= g.H {
					continue
				}
				j := n[1]*g.W + n[0]
				if !seen[j] && g.Data[j] >= thr {
					seen[j] = true
					stack = append(stack, int32(j))
				}
			}
		}
		c.Area = len(c.Pixels)
		if c.Area < minArea {
			continue
		}
		c.X /= wsum
		c.Y /= wsum
		cells = append(cells, c)
	}
	return cells
}
