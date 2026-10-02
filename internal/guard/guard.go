// Package guard stops the process before it overloads the machine it runs
// on. RainCast is meant to run on a laptop: when CPU, memory, disk or power
// cross a limit, the guard cancels the root context with the reason, and the
// program shuts down.
package guard

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

// Limits are the thresholds that stop the process. A zero value disables
// that check.
type Limits struct {
	// CPUPercent is the share of the whole machine (all cores) the process
	// may use, sustained for CPUSustain. Short bursts are allowed.
	CPUPercent float64       `json:"cpu_percent"`
	CPUSustain time.Duration `json:"cpu_sustain_ns"`
	// RSSBytes caps the process's resident memory.
	RSSBytes uint64 `json:"rss_bytes"`
	// MinFreeRAMPercent stops the process when the machine runs low on
	// memory, whoever is using it.
	MinFreeRAMPercent float64 `json:"min_free_ram_percent"`
	// MinFreeDisk is the free space required on the disk holding DiskPath.
	MinFreeDisk uint64 `json:"min_free_disk_bytes"`
	DiskPath    string `json:"-"`
	// MaxDirBytes caps the size of DirPath (the tile cache).
	MaxDirBytes uint64 `json:"max_dir_bytes"`
	DirPath     string `json:"-"`
	// StopOnBattery stops the process when the laptop is unplugged.
	StopOnBattery bool `json:"stop_on_battery"`
	MaxGoroutines int  `json:"max_goroutines"`
	// MaxProcs caps GOMAXPROCS; 0 leaves it alone.
	MaxProcs int `json:"max_procs"`
}

// DefaultLimits suit a laptop: half the cores, 2 GB of memory, and plenty of
// disk left for everything else.
func DefaultLimits(dataDir, tileDir string) Limits {
	return Limits{
		CPUPercent: 50, CPUSustain: 15 * time.Second,
		RSSBytes:          2 << 30,
		MinFreeRAMPercent: 15,
		MinFreeDisk:       20 << 30, DiskPath: dataDir,
		MaxDirBytes: 20 << 30, DirPath: tileDir,
		StopOnBattery: true,
		MaxGoroutines: 10_000,
		MaxProcs:      max(1, runtime.NumCPU()/2),
	}
}

// Apply sets the runtime limits that keep the process under the thresholds
// in the first place: fewer OS threads running Go code and a soft memory
// limit the GC works to stay under.
func (l Limits) Apply() {
	if l.MaxProcs > 0 {
		runtime.GOMAXPROCS(l.MaxProcs)
	}
	if l.RSSBytes > 0 {
		debug.SetMemoryLimit(int64(l.RSSBytes) * 3 / 4)
	}
}

// Sample is one reading of the machine and the process.
type Sample struct {
	At             time.Time `json:"at"`
	CPUPercent     float64   `json:"cpu_percent"` // of all cores
	RSS            uint64    `json:"rss_bytes"`
	FreeRAMPercent float64   `json:"free_ram_percent"`
	FreeDisk       uint64    `json:"free_disk_bytes"`
	DirBytes       uint64    `json:"dir_bytes"`
	OnBattery      bool      `json:"on_battery"`
	Goroutines     int       `json:"goroutines"`
}

// Sampler reads the current state.
type Sampler interface {
	Sample(ctx context.Context) (Sample, error)
}

// Violation is the error a Monitor cancels the context with.
type Violation struct {
	Metric string
	Value  string
	Limit  string
}

func (v *Violation) Error() string {
	return fmt.Sprintf("guard: %s = %s exceeds the limit %s", v.Metric, v.Value, v.Limit)
}

// Monitor samples periodically and reports the first limit crossed.
type Monitor struct {
	limits   Limits
	sampler  Sampler
	interval time.Duration
	log      *slog.Logger

	mu        sync.Mutex
	last      Sample
	cpuSince  time.Time // when CPU first went over the limit; zero if under
	violation *Violation
}

// NewMonitor returns a monitor that samples every interval.
func NewMonitor(l Limits, s Sampler, interval time.Duration, log *slog.Logger) *Monitor {
	return &Monitor{limits: l, sampler: s, interval: interval, log: log}
}

// Start applies l, then watches the process in the background. The returned
// context is canceled with a *Violation as its cause when a limit is
// crossed; every long-running goroutine should derive from it.
func Start(parent context.Context, l Limits, log *slog.Logger) (context.Context, *Monitor) {
	l.Apply()
	m := NewMonitor(l, NewSystemSampler(l.DiskPath, l.DirPath), 2*time.Second, log)
	ctx, cancel := context.WithCancelCause(parent)
	go m.Run(ctx, cancel)
	return ctx, m
}

// Run samples until ctx is done or a limit is crossed, then calls cancel.
func (m *Monitor) Run(ctx context.Context, cancel context.CancelCauseFunc) {
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		s, err := m.sampler.Sample(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.log.Warn("guard: sample failed", "err", err)
		} else if v := m.Check(s); v != nil {
			m.log.Error(v.Error())
			cancel(v)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Check records s and returns the first limit it crosses, if any.
func (m *Monitor) Check(s Sample) *Violation {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.last = s
	l := m.limits
	v := func(metric, value, limit string) *Violation {
		m.violation = &Violation{metric, value, limit}
		return m.violation
	}

	if l.CPUPercent > 0 && s.CPUPercent > l.CPUPercent {
		if m.cpuSince.IsZero() {
			m.cpuSince = s.At
		}
		if s.At.Sub(m.cpuSince) >= l.CPUSustain {
			return v("CPU", fmt.Sprintf("%.0f%% for %s", s.CPUPercent, s.At.Sub(m.cpuSince).Round(time.Second)),
				fmt.Sprintf("%.0f%% for %s", l.CPUPercent, l.CPUSustain))
		}
	} else {
		m.cpuSince = time.Time{}
	}
	switch {
	case l.RSSBytes > 0 && s.RSS > l.RSSBytes:
		return v("memory", gib(s.RSS), gib(l.RSSBytes))
	case l.MinFreeRAMPercent > 0 && s.FreeRAMPercent < l.MinFreeRAMPercent:
		return v("free RAM", fmt.Sprintf("%.1f%%", s.FreeRAMPercent), fmt.Sprintf("≥ %.0f%%", l.MinFreeRAMPercent))
	case l.MinFreeDisk > 0 && s.FreeDisk > 0 && s.FreeDisk < l.MinFreeDisk:
		return v("free disk", gib(s.FreeDisk), "≥ "+gib(l.MinFreeDisk))
	case l.MaxDirBytes > 0 && s.DirBytes > l.MaxDirBytes:
		return v("tile cache", gib(s.DirBytes), gib(l.MaxDirBytes))
	case l.StopOnBattery && s.OnBattery:
		return v("power", "on battery", "plugged in")
	case l.MaxGoroutines > 0 && s.Goroutines > l.MaxGoroutines:
		return v("goroutines", fmt.Sprint(s.Goroutines), fmt.Sprint(l.MaxGoroutines))
	}
	return nil
}

// Status is the latest sample next to the limits, for the admin page.
type Status struct {
	Sample    Sample `json:"sample"`
	Limits    Limits `json:"limits"`
	Violation string `json:"violation,omitempty"`
}

// Status returns the latest sample.
func (m *Monitor) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{Sample: m.last, Limits: m.limits}
	if m.violation != nil {
		st.Violation = m.violation.Error()
	}
	return st
}

func gib(b uint64) string { return fmt.Sprintf("%.2f GB", float64(b)/(1<<30)) }
