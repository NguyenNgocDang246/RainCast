package collect

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"raincast/internal/rainviewer"
)

// MapsFetcher loads RainViewer's frame index.
type MapsFetcher interface {
	FetchMaps(ctx context.Context) (*rainviewer.Maps, error)
}

// Poller keeps the latest frame index, for a collector running on its own.
type Poller struct {
	client MapsFetcher
	log    *slog.Logger

	mu     sync.RWMutex
	host   string
	frames []rainviewer.Frame
}

// NewPoller returns a poller; call Run to start it.
func NewPoller(client MapsFetcher, log *slog.Logger) *Poller {
	return &Poller{client: client, log: log}
}

// Run refreshes the index now and every interval until ctx is done.
func (p *Poller) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		maps, err := p.client.FetchMaps(rainviewer.Collecting(ctx))
		switch {
		case err != nil:
			if ctx.Err() == nil {
				p.log.Warn("collect: frame index", "err", err)
			}
		default:
			frames := append([]rainviewer.Frame(nil), maps.Radar.Past...)
			sort.Slice(frames, func(i, j int) bool { return frames[i].Time < frames[j].Time })
			p.mu.Lock()
			p.host, p.frames = maps.Host, frames
			p.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Feed returns the tile host and the latest frame index, oldest first.
func (p *Poller) Feed() (string, []rainviewer.Frame) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.host, p.frames
}
