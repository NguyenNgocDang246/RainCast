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

// trimLocked drops send times older than Per. Callers hold w.mu.
func (w *Window) trimLocked(now time.Time) {
	cut := 0
	for cut < len(w.sent) && now.Sub(w.sent[cut]) >= w.per {
		cut++
	}
	w.sent = w.sent[cut:]
}

// TryTake takes room for n requests if the window has it now, without
// waiting, and reports whether it did.
func (w *Window) TryTake(n int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	w.trimLocked(now)
	if len(w.sent)+n > w.n {
		return false
	}
	for range n {
		w.sent = append(w.sent, now)
	}
	return true
}

// Wait implements Waiter.
func (w *Window) Wait(ctx context.Context) error {
	for {
		w.mu.Lock()
		now := time.Now()
		w.trimLocked(now)
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
