package guard

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func ok(at time.Time) Sample {
	return Sample{At: at, CPUPercent: 10, RSS: 100 << 20, FreeRAMPercent: 60, FreeDisk: 100 << 30, Goroutines: 50}
}

func testLimits() Limits {
	return Limits{
		CPUPercent: 50, CPUSustain: 15 * time.Second, RSSBytes: 2 << 30,
		MinFreeRAMPercent: 15, MinFreeDisk: 20 << 30, MaxDirBytes: 20 << 30,
		StopOnBattery: true, MaxGoroutines: 10_000,
	}
}

func TestCheckEachLimit(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	cases := []struct {
		metric string
		mod    func(*Sample)
	}{
		{"memory", func(s *Sample) { s.RSS = 3 << 30 }},
		{"free RAM", func(s *Sample) { s.FreeRAMPercent = 5 }},
		{"free disk", func(s *Sample) { s.FreeDisk = 1 << 30 }},
		{"tile cache", func(s *Sample) { s.DirBytes = 25 << 30 }},
		{"power", func(s *Sample) { s.OnBattery = true }},
		{"goroutines", func(s *Sample) { s.Goroutines = 20_000 }},
	}
	for _, c := range cases {
		m := NewMonitor(testLimits(), nil, time.Second, quiet)
		if v := m.Check(ok(t0)); v != nil {
			t.Fatalf("healthy sample flagged: %v", v)
		}
		s := ok(t0)
		c.mod(&s)
		v := m.Check(s)
		if v == nil || v.Metric != c.metric {
			t.Errorf("%s: got %v", c.metric, v)
		}
	}
}

func TestCPUMustBeSustained(t *testing.T) {
	m := NewMonitor(testLimits(), nil, time.Second, quiet)
	t0 := time.Unix(1_000_000, 0)
	hot := func(sec int) Sample {
		s := ok(t0.Add(time.Duration(sec) * time.Second))
		s.CPUPercent = 90
		return s
	}
	for sec := 0; sec < 15; sec += 2 {
		if v := m.Check(hot(sec)); v != nil {
			t.Fatalf("stopped after only %ds: %v", sec, v)
		}
	}
	// Dropping below the limit restarts the count.
	if v := m.Check(ok(t0.Add(15 * time.Second))); v != nil {
		t.Fatal(v)
	}
	for sec := 16; sec < 30; sec += 2 {
		if v := m.Check(hot(sec)); v != nil {
			t.Fatalf("count was not reset: stopped at %ds", sec)
		}
	}
	if v := m.Check(hot(32)); v == nil || v.Metric != "CPU" {
		t.Fatalf("16s over the limit: got %v", v)
	}
}

func TestZeroLimitsDisableChecks(t *testing.T) {
	m := NewMonitor(Limits{}, nil, time.Second, quiet)
	s := Sample{At: time.Now(), CPUPercent: 100, RSS: 1 << 40, OnBattery: true, Goroutines: 1 << 20}
	if v := m.Check(s); v != nil {
		t.Fatal(v)
	}
}

type fakeSampler struct{ samples chan Sample }

func (f fakeSampler) Sample(ctx context.Context) (Sample, error) {
	select {
	case s := <-f.samples:
		return s, nil
	case <-ctx.Done():
		return Sample{}, ctx.Err()
	}
}

func TestRunCancelsWithViolation(t *testing.T) {
	f := fakeSampler{make(chan Sample, 2)}
	m := NewMonitor(testLimits(), f, time.Millisecond, quiet)
	ctx, cancel := context.WithCancelCause(context.Background())
	go m.Run(ctx, cancel)
	f.samples <- ok(time.Now())
	bad := ok(time.Now())
	bad.FreeRAMPercent = 1
	f.samples <- bad
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("context not canceled")
	}
	var v *Violation
	if !errors.As(context.Cause(ctx), &v) || v.Metric != "free RAM" {
		t.Fatalf("cause = %v", context.Cause(ctx))
	}
	if m.Status().Violation == "" {
		t.Error("status does not report the violation")
	}
}

func TestSystemSamplerReadsMachine(t *testing.T) {
	s := NewSystemSampler(t.TempDir(), t.TempDir())
	got, err := s.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.RSS == 0 || got.FreeRAMPercent <= 0 || got.FreeDisk == 0 || got.Goroutines == 0 {
		t.Errorf("implausible sample %+v", got)
	}
}

func TestKeepAwakeStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { KeepAwake(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("KeepAwake did not return after cancel")
	}
}
