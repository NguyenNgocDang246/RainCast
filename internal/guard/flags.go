package guard

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"time"
)

// Flags registers the limits on fs, starting from l; call the returned
// function after fs.Parse to get the final limits.
func Flags(fs *flag.FlagSet, l Limits) func() Limits {
	rssMB, freeDiskGB, tilesGB := l.RSSBytes>>20, l.MinFreeDisk>>30, l.MaxDirBytes>>30
	fs.Float64Var(&l.CPUPercent, "max-cpu", l.CPUPercent, "stop when the process uses more than this % of all CPU cores for 15 s (0 = no limit)")
	fs.Uint64Var(&rssMB, "max-memory-mb", rssMB, "stop when the process uses more memory than this (0 = no limit)")
	fs.Float64Var(&l.MinFreeRAMPercent, "min-free-ram", l.MinFreeRAMPercent, "stop when the machine has less free RAM than this % (0 = no limit)")
	fs.Uint64Var(&freeDiskGB, "min-free-disk-gb", freeDiskGB, "stop when the data disk has less free space than this (0 = no limit)")
	fs.Uint64Var(&tilesGB, "max-tiles-gb", tilesGB, "stop when the tile cache grows past this (0 = no limit)")
	fs.BoolVar(&l.StopOnBattery, "stop-on-battery", l.StopOnBattery, "stop when the laptop runs on battery")
	fs.IntVar(&l.MaxProcs, "max-procs", l.MaxProcs, "CPU cores Go may use at once (0 = all)")
	return func() Limits {
		l.RSSBytes, l.MinFreeDisk, l.MaxDirBytes = rssMB<<20, freeDiskGB<<30, tilesGB<<30
		return l
	}
}

// Log records the limits in force.
func (l Limits) Log(log *slog.Logger) {
	log.Info("guard", "max_procs", l.MaxProcs, "max_cpu_pct", l.CPUPercent, "max_memory_mb", l.RSSBytes>>20,
		"min_free_ram_pct", l.MinFreeRAMPercent, "min_free_disk_gb", l.MinFreeDisk>>30,
		"max_tiles_gb", l.MaxDirBytes>>30, "stop_on_battery", l.StopOnBattery)
}

// Stopped reports the violation ctx was canceled with, if any. When there
// is one it also arms a hard exit, so a shutdown that hangs does not keep
// loading the machine.
func Stopped(ctx context.Context, log *slog.Logger) *Violation {
	var v *Violation
	if !errors.As(context.Cause(ctx), &v) {
		return nil
	}
	time.AfterFunc(10*time.Second, func() {
		log.Error("guard: shutdown took too long, exiting")
		os.Exit(3)
	})
	return v
}
