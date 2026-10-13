package bench

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"raincast/internal/pipeline"
	"raincast/internal/store"
)

// Sink stores what the runner records (*store.Store is one).
type Sink interface {
	InsertBenchForecasts(ctx context.Context, rows []store.BenchForecast) error
	RecordBenchObs(ctx context.Context, o store.BenchObs) error
	InsertBenchMetar(ctx context.Context, reps []store.BenchMetar) error
}

// Runner asks every provider for a forecast at each station every Step,
// on the dot, so all of them are issued at the same moment.
type Runner struct {
	Stations  []pipeline.Station
	RainCast  *RainCast // nil: not recorded
	Providers []Provider
	// METAR, when set, records the reports of the stations named by an
	// ICAO code.
	METAR *METAR
	Sink  Sink
	Log       *slog.Logger
	// Settle is how long after each Step boundary the calls go out, so
	// providers have moved on to the new minute.
	Settle time.Duration

	paused map[string]time.Time // provider → until when it asked to stop
}

// Run polls until ctx is done.
func (r *Runner) Run(ctx context.Context) {
	for {
		slot := time.Now().Truncate(Step).Add(Step)
		t := time.NewTimer(time.Until(slot.Add(r.Settle)))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		r.Poll(ctx, slot)
	}
}

// Poll records every forecast issued at slot.
func (r *Runner) Poll(ctx context.Context, slot time.Time) {
	if r.paused == nil {
		r.paused = map[string]time.Time{}
	}
	r.recordMETAR(ctx)
	for _, st := range r.Stations {
		if r.RainCast != nil {
			samples, obs, err := r.RainCast.Forecast(ctx, st.Lat, st.Lon, slot)
			if err != nil {
				r.Log.Warn("raincast forecast", "station", st.ID, "err", err)
			} else if err := r.Sink.RecordBenchObs(ctx, store.BenchObs{Station: st.ID, Frame: obs.Frame.Unix(), DBZ: float64(obs.DBZ)}); err != nil {
				r.Log.Error("record observation", "err", err)
			}
			r.store(ctx, NameRainCast, st.ID, slot, samples)
		}
		for _, p := range r.Providers {
			name := p.Name()
			if until, ok := r.paused[name]; ok && slot.Before(until) {
				r.Log.Debug("provider paused", "provider", name, "until", until)
				continue
			}
			samples, err := p.Forecast(ctx, st.Lat, st.Lon, slot)
			switch wait, limited := RateLimited(err); {
			case errors.Is(err, ErrBudget):
				r.Log.Debug("provider budget spent", "provider", name)
			case limited:
				if wait <= 0 {
					wait = time.Hour
				}
				r.paused[name] = slot.Add(wait)
				r.Log.Warn("provider rate limited", "provider", name, "pause", wait)
			case err != nil:
				r.Log.Warn("provider forecast", "provider", name, "station", st.ID, "err", err)
			}
			r.store(ctx, name, st.ID, slot, samples)
		}
	}
}

// recordMETAR stores the stations' reports of the last 2 hours; repeats are
// kept once, and a missed poll is caught up by the next.
func (r *Runner) recordMETAR(ctx context.Context) {
	if r.METAR == nil {
		return
	}
	var ids []string
	for _, st := range r.Stations {
		if IsICAO(st.ID) {
			ids = append(ids, st.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	reps, err := r.METAR.Recent(ctx, ids, 2)
	if err != nil {
		r.Log.Warn("metar", "err", err)
		return
	}
	if err := r.Sink.InsertBenchMetar(ctx, reps); err != nil {
		r.Log.Error("store metar", "err", err)
	}
}

func (r *Runner) store(ctx context.Context, provider, station string, slot time.Time, samples []Sample) {
	if len(samples) == 0 {
		return
	}
	rows := make([]store.BenchForecast, len(samples))
	for i, s := range samples {
		rows[i] = store.BenchForecast{
			Provider: provider, Station: station, Issued: slot.Unix(), Valid: s.Valid.Unix(), MMH: s.MMH, Kind: s.Kind,
		}
	}
	if err := r.Sink.InsertBenchForecasts(ctx, rows); err != nil {
		r.Log.Error("store forecasts", "provider", provider, "err", err)
		return
	}
	r.Log.Debug("recorded", "provider", provider, "station", station, "samples", len(samples), "kind", samples[0].Kind)
}

// MonthlyCap stops calling Provider once Limit calls went out in the
// calendar month (UTC), returning ErrBudget instead.
type MonthlyCap struct {
	Provider
	Limit int
	// Count, when set, counts the calls already made since a time (calls
	// before a restart).
	Count func(ctx context.Context, since time.Time) (int, error)

	month time.Time
	used  int
}

// Forecast implements Provider.
func (c *MonthlyCap) Forecast(ctx context.Context, lat, lon float64, now time.Time) ([]Sample, error) {
	if m := monthStart(now); !m.Equal(c.month) {
		c.month, c.used = m, 0
		if c.Count != nil {
			if n, err := c.Count(ctx, m); err == nil {
				c.used = n
			}
		}
	}
	if c.used >= c.Limit {
		return nil, ErrBudget
	}
	c.used++
	return c.Provider.Forecast(ctx, lat, lon, now)
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
