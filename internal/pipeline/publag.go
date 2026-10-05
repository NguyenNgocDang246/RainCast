package pipeline

import (
	"context"
	"encoding/json"
	"time"
)

// FrameInterval is how often RainViewer adds a radar frame (at :00, :10, …).
const FrameInterval = 10 * time.Minute

const (
	// lagSamples is how many recent publish delays are kept.
	lagSamples = 6
	// lagWatchGap is the most two indexes may be generated apart for the
	// frame that appears between them to give its delay.
	lagWatchGap = 3 * time.Minute
	// sharedLagTTL keeps the delays for processes that start later.
	sharedLagTTL = 24 * time.Hour
	sharedLagKey = "rc:publag"
)

// lagTracker learns how long after its own time a radar frame shows up in
// RainViewer's index, from the generated time of the first index that has
// it.
type lagTracker struct {
	newest    int64   // newest frame seen, unix seconds
	generated int64   // generated time of the latest index that had it
	samples   []int64 // delays in seconds, oldest first
}

// observe records an index generated at gen whose newest frame is newest,
// and reports whether it measured a delay. Only a frame that appears
// between two indexes generated close together is measured: otherwise it
// may have been there long before it was seen.
func (l *lagTracker) observe(newest, gen int64) bool {
	if gen == 0 || newest < l.newest || gen < l.generated {
		return false
	}
	prevNewest, prevGen := l.newest, l.generated
	l.newest, l.generated = newest, gen
	if prevNewest == 0 || newest-prevNewest != int64(FrameInterval/time.Second) ||
		gen-prevGen > int64(lagWatchGap/time.Second) {
		return false
	}
	d := gen - newest
	if d < 0 || d > int64(FrameInterval/time.Second) {
		return false
	}
	l.samples = append(l.samples, d)
	if len(l.samples) > lagSamples {
		l.samples = l.samples[len(l.samples)-lagSamples:]
	}
	return true
}

// lag is the shortest recent delay, so the next frame is watched for from
// about when it may first appear; 0 before any is measured.
func (l *lagTracker) lag() time.Duration {
	if len(l.samples) == 0 {
		return 0
	}
	m := l.samples[0]
	for _, s := range l.samples[1:] {
		m = min(m, s)
	}
	return time.Duration(m) * time.Second
}

// NextDue is when the frame after the one at t is expected in the index:
// its time plus how late frames have lately been published.
func (p *Pipeline) NextDue(t time.Time) time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.nextDue(t)
}

// nextDue is NextDue for callers holding p.mu.
func (p *Pipeline) nextDue(t time.Time) time.Time {
	return t.Add(FrameInterval + p.lags.lag())
}

// observeLag feeds an index to the tracker, sharing the delays with other
// processes (and starting from theirs) when Config.Shared is set.
func (p *Pipeline) observeLag(ctx context.Context, newest, gen int64) {
	p.mu.Lock()
	measured := p.lags.observe(newest, gen)
	empty := len(p.lags.samples) == 0
	samples := append([]int64(nil), p.lags.samples...)
	p.mu.Unlock()
	if p.cfg.Shared == nil {
		return
	}
	switch {
	case measured:
		if data, err := json.Marshal(samples); err == nil {
			p.cfg.Shared.Set(ctx, sharedLagKey, data, sharedLagTTL)
		}
	case empty:
		data, ok := p.cfg.Shared.Get(ctx, sharedLagKey)
		var shared []int64
		if !ok || json.Unmarshal(data, &shared) != nil {
			return
		}
		p.mu.Lock()
		if len(p.lags.samples) == 0 {
			p.lags.samples = shared
		}
		p.mu.Unlock()
	}
}
