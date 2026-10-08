package cell

// Options tunes the tracker.
type Options struct {
	Threshold float32 // dBZ outlining a cell
	MinArea   int     // pixels
	Gate      float64 // max distance, in pixels, from a cell's predicted position
	Match     Matcher
}

// DefaultOptions outlines convective cells at 30 dBZ. The gate allows a
// ~12 px (15 km) miss after the area motion has been applied.
func DefaultOptions(match Matcher) Options {
	return Options{Threshold: 30, MinArea: 16, Gate: 12, Match: match}
}
