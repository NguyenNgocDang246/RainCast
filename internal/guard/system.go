package guard

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

// dirEvery is how often the tile cache is measured: walking millions of
// tiles every two seconds would itself load the machine.
const dirEvery = 10 * time.Minute

// SystemSampler reads the real process and machine.
type SystemSampler struct {
	proc     *process.Process
	diskPath string
	dirPath  string

	dirBytes atomic.Uint64
	dirMu    sync.Mutex // one walk at a time
	dirAt    time.Time
}

// NewSystemSampler measures free space on diskPath's disk and the size of
// dirPath; either may be empty.
func NewSystemSampler(diskPath, dirPath string) *SystemSampler {
	p, _ := process.NewProcess(int32(os.Getpid()))
	return &SystemSampler{proc: p, diskPath: diskPath, dirPath: dirPath}
}

// Sample implements Sampler.
func (s *SystemSampler) Sample(ctx context.Context) (Sample, error) {
	out := Sample{At: time.Now(), Goroutines: runtime.NumGoroutine()}
	if s.proc != nil {
		// Percent(0) is the CPU time since the previous call, as a share of
		// one core.
		if pct, err := s.proc.PercentWithContext(ctx, 0); err == nil {
			out.CPUPercent = pct / float64(runtime.NumCPU())
		}
		if mi, err := s.proc.MemoryInfoWithContext(ctx); err == nil {
			out.RSS = mi.RSS
		}
	}
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return out, err
	}
	if vm.Total > 0 {
		out.FreeRAMPercent = float64(vm.Available) / float64(vm.Total) * 100
	}
	if s.diskPath != "" {
		if u, err := disk.UsageWithContext(ctx, existingParent(s.diskPath)); err == nil {
			out.FreeDisk = u.Free
		}
	}
	if s.dirPath != "" {
		s.maybeWalk()
		out.DirBytes = s.dirBytes.Load()
	}
	out.OnBattery = onBattery()
	return out, nil
}

// maybeWalk re-measures the directory in the background when the last
// measurement is stale; the first one runs synchronously.
func (s *SystemSampler) maybeWalk() {
	if !s.dirMu.TryLock() {
		return
	}
	first := s.dirAt.IsZero()
	if !first && time.Since(s.dirAt) < dirEvery {
		s.dirMu.Unlock()
		return
	}
	s.dirAt = time.Now()
	walk := func() {
		defer s.dirMu.Unlock()
		s.dirBytes.Store(dirSize(s.dirPath))
	}
	if first {
		walk()
	} else {
		go walk()
	}
}

func dirSize(root string) uint64 {
	var n uint64
	filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			n += uint64(info.Size())
		}
		return nil
	})
	return n
}

// existingParent returns path or its nearest existing ancestor, so free
// space can be read before the data directory is created.
func existingParent(path string) string {
	p, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}
