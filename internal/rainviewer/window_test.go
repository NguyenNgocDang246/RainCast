package rainviewer

import (
	"context"
	"testing"
	"time"
)

// A burst within the cap goes out at once; the request over it waits for
// the oldest to leave the window.
func TestWindowAllowsBurstThenWaits(t *testing.T) {
	w := NewWindow(5, 300*time.Millisecond)
	ctx := context.Background()
	start := time.Now()
	for range 5 {
		if err := w.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("burst of 5 took %s, want immediate", d)
	}
	if err := w.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 280*time.Millisecond {
		t.Fatalf("6th request after %s, want ≥ the 300 ms window", d)
	}
}

func TestWindowHonorsContext(t *testing.T) {
	w := NewWindow(1, time.Hour)
	w.Wait(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := w.Wait(ctx); err == nil {
		t.Fatal("waited past the context deadline")
	}
}

func TestWindowTryTake(t *testing.T) {
	w := NewWindow(5, 300*time.Millisecond)
	if !w.TryTake(4) {
		t.Fatal("4 of 5 refused")
	}
	if w.TryTake(2) {
		t.Fatal("took 2 with 1 left")
	}
	if !w.TryTake(1) {
		t.Fatal("last one refused")
	}
	time.Sleep(320 * time.Millisecond)
	if !w.TryTake(5) {
		t.Fatal("window did not empty")
	}
}
