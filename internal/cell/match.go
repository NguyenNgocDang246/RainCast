package cell

import (
	"math"

	"raincast/internal/motion"
)

// Pair links cell Prev in the earlier frame to cell Cur in the later one.
type Pair struct{ Prev, Cur int }

// Matcher assigns cells of one frame to the next.
type Matcher func(prev, cur []Cell, field *motion.Field, minutes, gate float64) []Pair

// overlapWeight scales the (1 − IoU) part of the cost against distance.
const overlapWeight = 0.5

// cost is how unlikely cur is to be prev a frame later: the distance from
// where prev is predicted to be (moved along the area's motion), relative
// to the gate, plus how little their predicted boxes overlap. Beyond the
// gate the pair is impossible.
func cost(p, c Cell, field *motion.Field, minutes, gate float64) float64 {
	dx, dy := 0.0, 0.0
	if field != nil {
		v := field.At(p.X, p.Y)
		dx, dy = v.DX*minutes, v.DY*minutes
	}
	d := math.Hypot(c.X-(p.X+dx), c.Y-(p.Y+dy))
	if d > gate {
		return math.Inf(1)
	}
	return d/gate + overlapWeight*(1-boxIoU(p, c, dx, dy))
}

// boxIoU is the overlap of p's box shifted by (dx, dy) with c's box.
func boxIoU(p, c Cell, dx, dy float64) float64 {
	ax0, ay0 := float64(p.MinX)+dx, float64(p.MinY)+dy
	ax1, ay1 := float64(p.MaxX+1)+dx, float64(p.MaxY+1)+dy
	bx0, by0, bx1, by1 := float64(c.MinX), float64(c.MinY), float64(c.MaxX+1), float64(c.MaxY+1)
	iw := math.Min(ax1, bx1) - math.Max(ax0, bx0)
	ih := math.Min(ay1, by1) - math.Max(ay0, by0)
	if iw <= 0 || ih <= 0 {
		return 0
	}
	inter := iw * ih
	return inter / ((ax1-ax0)*(ay1-ay0) + (bx1-bx0)*(by1-by0) - inter)
}

// MatchNearest links every later cell to the cheapest earlier cell within
// the gate. Several later cells may claim the same earlier one (a split),
// which is also how it goes wrong when cells pass close by each other.
func MatchNearest(prev, cur []Cell, field *motion.Field, minutes, gate float64) []Pair {
	var out []Pair
	for j, c := range cur {
		best, bi := math.Inf(1), -1
		for i, p := range prev {
			if k := cost(p, c, field, minutes, gate); k < best {
				best, bi = k, i
			}
		}
		if bi >= 0 {
			out = append(out, Pair{bi, j})
		}
	}
	return out
}

// MatchHungarian finds the one-to-one assignment with the lowest total
// cost; cells with no partner inside the gate stay unmatched.
func MatchHungarian(prev, cur []Cell, field *motion.Field, minutes, gate float64) []Pair {
	n, m := len(prev), len(cur)
	if n == 0 || m == 0 {
		return nil
	}
	// Square matrix: real pairs, plus "unmatched" slots whose cost is just
	// above any allowed pair, so leaving a cell alone beats a forced bad
	// match.
	const none = 1 + overlapWeight + 0.01
	size := n + m
	c := make([][]float64, size)
	for i := range size {
		c[i] = make([]float64, size)
		for j := range size {
			switch {
			case i < n && j < m:
				k := cost(prev[i], cur[j], field, minutes, gate)
				if math.IsInf(k, 1) {
					k = 1e6
				}
				c[i][j] = k
			case i < n || j < m:
				c[i][j] = none
			}
		}
	}
	assign := hungarian(c)
	var out []Pair
	for i := range n {
		j := assign[i]
		if j < m && c[i][j] < 1e6 {
			out = append(out, Pair{i, j})
		}
	}
	return out
}

// hungarian solves the square assignment problem (Kuhn–Munkres with
// potentials, O(n³)); it returns the column assigned to each row.
func hungarian(c [][]float64) []int {
	n := len(c)
	inf := math.Inf(1)
	u, v := make([]float64, n+1), make([]float64, n+1)
	p, way := make([]int, n+1), make([]int, n+1)
	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, n+1)
		used := make([]bool, n+1)
		for j := range minv {
			minv[j] = inf
		}
		for {
			used[j0] = true
			i0, delta, j1 := p[j0], inf, 0
			for j := 1; j <= n; j++ {
				if used[j] {
					continue
				}
				cur := c[i0-1][j-1] - u[i0] - v[j]
				if cur < minv[j] {
					minv[j], way[j] = cur, j0
				}
				if minv[j] < delta {
					delta, j1 = minv[j], j
				}
			}
			for j := 0; j <= n; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for j0 != 0 {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
		}
	}
	out := make([]int, n)
	for j := 1; j <= n; j++ {
		if p[j] > 0 {
			out[p[j]-1] = j - 1
		}
	}
	return out
}
