// Package progress estimates when a long run ends, for its log lines.
package progress

import (
	"fmt"
	"time"
)

// Estimate is how far a run is and, from its pace so far, how long it has
// left; Left is negative until there is a pace to go by.
type Estimate struct {
	Done, Total int64
	Elapsed     time.Duration
	Left        time.Duration
}

// New estimates from done of total units in elapsed.
func New(done, total int64, elapsed time.Duration) Estimate {
	e := Estimate{Done: done, Total: total, Elapsed: elapsed, Left: -1}
	if done > 0 && total >= done {
		e.Left = time.Duration(float64(elapsed) * float64(total-done) / float64(done))
	}
	return e
}

// Args are slog attributes: done/total, percent, elapsed, time left and
// the local clock time it should end.
func (e Estimate) Args() []any {
	pct := 0.0
	if e.Total > 0 {
		pct = 100 * float64(e.Done) / float64(e.Total)
	}
	args := []any{"done", fmt.Sprintf("%d/%d", e.Done, e.Total), "pct", fmt.Sprintf("%.1f%%", pct),
		"elapsed", e.Elapsed.Round(time.Second)}
	if e.Left >= 0 {
		args = append(args, "left", e.Left.Round(time.Second), "eta", time.Now().Add(e.Left).Format("15:04"))
	} else {
		args = append(args, "left", "?")
	}
	return args
}
