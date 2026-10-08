package backtest

import (
	"context"
	"slices"

	"raincast/internal/radar"
)

// Run scores in-memory frames as a single region.
func Run(frames []Frame, cfg Config) Report {
	rep, _ := RunRegions(context.Background(), []Region{{Name: "local", Source: memSource(frames)}}, cfg, nil)
	return rep
}

// memSource serves frames already in memory.
type memSource []Frame

func (m memSource) Times() []int64 {
	out := make([]int64, len(m))
	for i, f := range m {
		out[i] = f.Time
	}
	slices.Sort(out)
	return out
}

func (m memSource) Load(t int64) *radar.Mosaic {
	for _, f := range m {
		if f.Time == t {
			return f.Mosaic
		}
	}
	return nil
}
