package nwp

import (
	"context"
	"sync"
	"time"
)

// Window allows at most N calls in any span of Per, the way Open-Meteo
// counts them: a request for n locations is n calls. A burst goes out at
// once while the window has room; beyond the cap a request waits for the
// oldest calls to age out.
type Window struct {
	n   int
	per time.Duration

	mu   sync.Mutex
	sent []sent // within the last Per, oldest first
	used int    // calls in sent
}

type sent struct {
	t time.Time
	n int
}

// NewWindow allows n calls per span.
func NewWindow(n int, per time.Duration) *Window {
	return &Window{n: max(n, 1), per: per}
}

// WaitN blocks until n more calls fit in the window; n above the cap is
// let through alone once the window is empty.
func (w *Window) WaitN(ctx context.Context, n int) error {
	for {
		w.mu.Lock()
		now := time.Now()
		cut := 0
		for cut < len(w.sent) && now.Sub(w.sent[cut].t) >= w.per {
			w.used -= w.sent[cut].n
			cut++
		}
		w.sent = w.sent[cut:]
		if w.used+n <= w.n || w.used == 0 {
			w.sent = append(w.sent, sent{now, n})
			w.used += n
			w.mu.Unlock()
			return nil
		}
		wait := w.sent[0].t.Add(w.per).Sub(now) // the oldest ages out
		w.mu.Unlock()
		t := time.NewTimer(max(wait, time.Millisecond))
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
