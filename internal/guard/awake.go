package guard

import (
	"context"
	"time"
)

// awakeEvery is how often the idle-sleep timer is reset; Windows' shortest
// sleep timeout is one minute.
const awakeEvery = 30 * time.Second

// KeepAwake stops the machine from going to sleep on its own until ctx is
// done, so collection has no gaps (RainViewer keeps only two hours of
// frames). The display may still turn off, and closing the lid or choosing
// Sleep still works. It does nothing where not supported.
func KeepAwake(ctx context.Context) {
	if !preventSleep() {
		return
	}
	t := time.NewTicker(awakeEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			preventSleep()
		}
	}
}
