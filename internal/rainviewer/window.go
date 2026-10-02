package rainviewer

import (
	"context"
	"sync"
	"time"
)

// Waiter blocks until one more request may be sent. *rate.Limiter is one.
type Waiter interface {
	Wait(ctx context.Context) error
}

// Window allows at most N requests in any span of Per, the way RainViewer
// counts them: a burst goes out at once while the window has room, and
// only requests beyond the cap wait for the oldest to age out.
type Window struct {
	n   int
	per time.Duration

	mu   sync.Mutex
	sent []time.Time // send times within the last Per, oldest first
}

// NewWindow allows n requests per span.
func NewWindow(n int, per time.Duration) *Window {
	return &Window{n: max(n, 1), per: per}
}

// Wait implements Waiter.
func (w *Window) Wait(ctx context.Context) error {
	for {
		w.mu.Lock()
		now := time.Now()
		cut := 0
		for cut < len(w.sent) && now.Sub(w.sent[cut]) >= w.per {
			cut++
		}
		w.sent = w.sent[cut:]
		if len(w.sent) < w.n {
			w.sent = append(w.sent, now)
			w.mu.Unlock()
			return nil
		}
		wait := w.sent[0].Add(w.per).Sub(now)
		w.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
