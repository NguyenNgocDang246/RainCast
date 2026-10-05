package cell

import (
	"math"

	"raincast/internal/motion"
	"raincast/internal/radar"
)

// Link says cell Prev of one frame became (part of) cell Cur of the next.
type Link struct{ Prev, Cur int }

// minOverlap is the share of the smaller cell that must overlap for two
// cells to be linked: enough to ignore cells merely passing nearby.
const minOverlap = 0.3

// MatchOverlap links cells by area: each earlier cell's pixels are moved
// along field for minutes and laid over the later cells. Unlike a
// one-to-one assignment it keeps every link, so one earlier cell may feed
// several later ones (a split) and several earlier ones one later (a
// merge). w and h are the grid size the pixel indices refer to.
func MatchOverlap(prev, cur []Cell, w, h int, field *motion.Field, minutes float64) []Link {
	label := make([]int32, w*h)
	for i := range label {
		label[i] = -1
	}
	for j, c := range cur {
		for _, p := range c.Pixels {
			label[p] = int32(j)
		}
	}
	var out []Link
	count := map[int32]int{}
	for i, c := range prev {
		clear(count)
		for _, p := range c.Pixels {
			x, y := float64(int(p)%w), float64(int(p)/w)
			if field != nil {
				v := field.At(x, y)
				x, y = x+v.DX*minutes, y+v.DY*minutes
			}
			xi, yi := int(math.Round(x)), int(math.Round(y))
			if xi < 0 || yi < 0 || xi >= w || yi >= h {
				continue
			}
			if j := label[yi*w+xi]; j >= 0 {
				count[j]++
			}
		}
		for j, n := range count {
			if float64(n) >= minOverlap*float64(min(c.Area, cur[j].Area)) {
				out = append(out, Link{i, int(j)})
			}
		}
	}
	return out
}

// Storm is a cell of the newest frame with its recent life.
type Storm struct {
	Cell
	Age int // frames it can be followed back through, 1 for a new cell
	// MeanDBZ is the cell's mean echo now; RateDBZ and RateArea how its
	// mean echo (dBZ/min) and area (px/min) changed along its main
	// ancestors (0 for a new cell).
	MeanDBZ  float64
	RateDBZ  float64
	RateArea float64
	New      bool // nothing earlier overlaps it: it formed since the last frame
	Merged   bool // two or more cells of the last frame flowed into it
	Split    bool // its main ancestor also fed another cell of this frame
	Decaying bool // shrinking and weakening
}

// Lifecycle follows the cells of the newest frame back through frames
// (oldest first, minutes apart), moving them along field between frames.
// Each cell's history runs through its main ancestor, the earlier cell
// overlapping it most.
func Lifecycle(frames []*radar.Grid, field *motion.Field, minutes float64, opt Options) []Storm {
	if len(frames) == 0 {
		return nil
	}
	w, h := frames[0].W, frames[0].H
	cells := make([][]Cell, len(frames))
	for i, g := range frames {
		cells[i] = Segment(g, opt.Threshold, opt.MinArea)
	}
	mean := func(k int, c Cell) float64 {
		var s float64
		for _, p := range c.Pixels {
			s += float64(frames[k].Data[p])
		}
		return s / float64(c.Area)
	}
	// parents[k][j]: cells of frame k-1 linked to cell j of frame k;
	// children[k][i]: how many cells of frame k cell i of frame k-1 feeds.
	parents := make([]map[int][]int, len(frames))
	children := make([]map[int]int, len(frames))
	for k := 1; k < len(frames); k++ {
		parents[k], children[k] = map[int][]int{}, map[int]int{}
		for _, l := range MatchOverlap(cells[k-1], cells[k], w, h, field, minutes) {
			parents[k][l.Cur] = append(parents[k][l.Cur], l.Prev)
			children[k][l.Prev]++
		}
	}
	// main is the parent of cell j in frame k sharing the most area with it
	// (the largest, which is what overlaps most once moved).
	main := func(k, j int) int {
		best, bi := -1, -1
		for _, i := range parents[k][j] {
			if a := cells[k-1][i].Area; a > best {
				best, bi = a, i
			}
		}
		return bi
	}

	last := len(frames) - 1
	out := make([]Storm, 0, len(cells[last]))
	for j, c := range cells[last] {
		s := Storm{Cell: c, Age: 1, MeanDBZ: mean(last, c)}
		if last > 0 {
			s.New = len(parents[last][j]) == 0
			s.Merged = len(parents[last][j]) >= 2
			if p := main(last, j); p >= 0 {
				s.Split = children[last][p] >= 2
			}
		}
		// Mean echo and area along the main ancestors, newest first.
		ts, zs, as := []float64{0}, []float64{s.MeanDBZ}, []float64{float64(c.Area)}
		idx := j
		for k := last; k >= 1; k-- {
			p := main(k, idx)
			if p < 0 {
				break
			}
			pc := cells[k-1][p]
			ts = append(ts, -float64(last-k+1)*minutes)
			zs = append(zs, mean(k-1, pc))
			as = append(as, float64(pc.Area))
			idx = p
		}
		s.Age = len(ts)
		if s.Age >= 2 {
			s.RateDBZ = slope(ts, zs)
			s.RateArea = slope(ts, as)
			s.Decaying = s.RateDBZ < 0 && s.RateArea < 0
		}
		out = append(out, s)
	}
	return out
}

// slope is the least-squares slope of ys on xs.
func slope(xs, ys []float64) float64 {
	n := float64(len(xs))
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
	}
	d := n*sxx - sx*sx
	if d == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / d
}
