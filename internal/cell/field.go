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

// cellBlocks averages tracked cell velocities over the blocks their pixels
// cover; a block is measured when cells cover at least 5% of it.
func cellBlocks(w, h, bs int, cells []Cell, vel []motion.Vector) ([]motion.Vector, []bool) {
	bw, bh := w/bs, h/bs
	sum := make([]motion.Vector, bw*bh)
	n := make([]int, bw*bh)
	for ci, c := range cells {
		for _, i := range c.Pixels {
			bx, by := int(i)%w/bs, int(i)/w/bs
			if bx >= bw || by >= bh {
				continue
			}
			b := by*bw + bx
			sum[b].DX += vel[ci].DX
			sum[b].DY += vel[ci].DY
			n[b]++
		}
	}
	valid := make([]bool, bw*bh)
	for b := range sum {
		if n[b] >= max(1, bs*bs/20) {
			sum[b].DX /= float64(n[b])
			sum[b].DY /= float64(n[b])
			valid[b] = true
		} else {
			sum[b] = motion.Vector{}
		}
	}
	return sum, valid
}

// Field is the cell-only motion: tracked cell velocities, filled in between
// cells from the nearest ones.
func Field(frames []*radar.Grid, pairs []*motion.Field, minutes float64, opt Options) *motion.Field {
	cur := frames[len(frames)-1]
	cells, vel := Track(frames, pairs, minutes, opt)
	v, valid := cellBlocks(cur.W, cur.H, opt.BlockSize, cells, vel)
	return motion.FromBlocks(opt.BlockSize, cur.W/opt.BlockSize, cur.H/opt.BlockSize, v, valid)
}

// Hybrid is base (the area motion) with tracked cells moving at their own
// velocity: storms that drift across the steering flow keep their course,
// and everything else follows base. base must use opt.BlockSize.
func Hybrid(frames []*radar.Grid, pairs []*motion.Field, base *motion.Field, minutes float64, opt Options) *motion.Field {
	cur := frames[len(frames)-1]
	cells, vel := Track(frames, pairs, minutes, opt)
	v, valid := cellBlocks(cur.W, cur.H, base.BlockSize, cells, vel)
	out := &motion.Field{BlockSize: base.BlockSize, BW: base.BW, BH: base.BH,
		V: append([]motion.Vector(nil), base.V...), Valid: append([]bool(nil), base.Valid...),
		Global: base.Global, NValid: base.NValid}
	for b := range out.V {
		if b < len(valid) && valid[b] {
			out.V[b] = v[b]
			if !out.Valid[b] {
				out.Valid[b] = true
				out.NValid++
			}
		}
	}
	return out
}
