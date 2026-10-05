package pipeline

import (
	"testing"
	"time"

	"raincast/internal/rainviewer"
)

func TestMaxAgeShortensOnceNextFrameDue(t *testing.T) {
	frame := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	p := &Pipeline{frames: []rainviewer.Frame{{Time: frame.Unix()}}}
	if got := p.maxAge(2*time.Minute, frame.Add(9*time.Minute)); got != 2*time.Minute {
		t.Errorf("before the next frame is due: %v", got)
	}
	if got := p.maxAge(2*time.Minute, frame.Add(12*time.Minute)); got != overdueMaxAge {
		t.Errorf("once it is due: %v", got)
	}
	// Frames lately show up 3 minutes late: not due yet at :12.
	p.lags.samples = []int64{180, 240}
	if got := p.maxAge(2*time.Minute, frame.Add(12*time.Minute)); got != 2*time.Minute {
		t.Errorf("before a late frame is due: %v", got)
	}
	if got := (&Pipeline{}).maxAge(2*time.Minute, frame); got != 2*time.Minute {
		t.Errorf("without frames: %v", got)
	}
}

func TestSharedIndexAge(t *testing.T) {
	due := time.Date(2026, 10, 5, 8, 10, 0, 0, time.UTC)
	for _, c := range []struct {
		before, want time.Duration
	}{
		{8 * time.Minute, sharedIndexTTL},
		{15 * time.Second, overdueMaxAge}, // due in 15s: still kept 30s
		{40 * time.Second, 40 * time.Second},
		{-5 * time.Minute, overdueMaxAge}, // overdue
	} {
		if got := sharedIndexAge(due, due.Add(-c.before)); got != c.want {
			t.Errorf("%v before due: %v, want %v", c.before, got, c.want)
		}
	}
}

func TestLagTracker(t *testing.T) {
	const f0 = 1_790_000_400 // a frame time, on the 10 minutes
	var l lagTracker
	if l.observe(f0, f0+200) {
		t.Fatal("the first index measured a delay")
	}
	l.observe(f0, f0+760) // still the same frame, 2:40 after the next one's time
	// The next frame appears in an index generated 30s later: 190s late.
	if !l.observe(f0+600, f0+790) || l.lag() != 190*time.Second {
		t.Fatalf("lag = %v, samples %v", l.lag(), l.samples)
	}
	// Seen only long after the last index: when it appeared is unknown.
	if l.observe(f0+1200, f0+1200+600+170) {
		t.Fatal("measured across a gap")
	}
	// A skipped frame, an older index and a missing generated time are ignored.
	if l.observe(f0+2400, f0+2400+100) || l.observe(f0, f0+3000) || l.observe(f0+3000, 0) {
		t.Fatal("measured a frame it did not watch for")
	}
	// The shortest recent delay wins, and only the last lagSamples count.
	l = lagTracker{samples: []int64{90, 300, 200, 250, 260, 270}}
	if l.lag() != 90*time.Second {
		t.Fatalf("lag = %v", l.lag())
	}
	l.newest, l.generated = f0, f0+680
	l.observe(f0+600, f0+700)
	if len(l.samples) != lagSamples || l.lag() != 100*time.Second {
		t.Fatalf("samples %v, lag %v", l.samples, l.lag())
	}
}
