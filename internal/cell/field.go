package cell

import (
	"raincast/internal/motion"
	"raincast/internal/radar"
)

// Options tunes the tracker.
type Options struct {
	Threshold float32 // dBZ outlining a cell
	MinArea   int     // pixels
	Gate      float64 // max distance, in pixels, from a cell's predicted position
	Match     Matcher
	BlockSize int // output field block size
}

// DefaultOptions outlines convective cells at 30 dBZ. The gate allows a
// ~12 px (15 km) miss after the area motion has been applied.
func DefaultOptions(match Matcher) Options {
	return Options{Threshold: 30, MinArea: 16, Gate: 12, Match: match, BlockSize: 32}
}

// Track follows the cells of the newest frame back through frames (oldest
// first, minutes apart) and returns each tracked cell with its velocity in
// pixels per minute. pairs[k] is the area motion from frames[k] to
// frames[k+1], used to predict where cells go before matching.
func Track(frames []*radar.Grid, pairs []*motion.Field, minutes float64, opt Options) ([]Cell, []motion.Vector) {
	cells := make([][]Cell, len(frames))
	for i, g := range frames {
		cells[i] = Segment(g, opt.Threshold, opt.MinArea)
	}
	// back[k][j] is the cell in frames[k-1] that cell j of frames[k] came from.
	back := make([]map[int]int, len(frames))
	for k := 1; k < len(frames); k++ {
		back[k] = map[int]int{}
		for _, p := range opt.Match(cells[k-1], cells[k], pairs[k-1], minutes, opt.Gate) {
			back[k][p.Cur] = p.Prev
		}
	}
	last := len(frames) - 1
	var out []Cell
	var vel []motion.Vector
	for j, c := range cells[last] {
		xs, ys := []float64{c.X}, []float64{c.Y}
		idx := j
		for k := last; k >= 1; k-- {
			i, ok := back[k][idx]
			if !ok {
				break
			}
			p := cells[k-1][i]
			xs, ys = append(xs, p.X), append(ys, p.Y)
			idx = i
		}
		if len(xs) < 2 {
			continue // new cell: no history yet
		}
		reverse(xs)
		reverse(ys)
		// Start from the area motion where the track began.
		v0 := pairs[last-len(xs)+1].At(xs[0], ys[0])
		vx, vy := trackVelocity(xs, ys, minutes, v0.DX, v0.DY)
		out = append(out, c)
		vel = append(vel, motion.Vector{DX: vx, DY: vy})
	}
	return out, vel
}

func reverse(s []float64) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
