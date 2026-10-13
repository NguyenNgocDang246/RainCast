package bench

import (
	"context"
	"time"

	"raincast/internal/pipeline"
	"raincast/internal/radar"
)

// RainCast is this app's forecast, as a user asking at that moment gets it:
// from the newest RainViewer frame published, which is already some minutes
// old. The pipeline's horizon must reach Horizon past that lag (the app's
// 60 minutes run from the frame, not from now).
type RainCast struct {
	P *pipeline.Pipeline
}

// Forecast returns RainCast's samples from now on, and the radar echo at
// the point in the frame they start from (RainViewer's observation).
func (r *RainCast) Forecast(ctx context.Context, lat, lon float64, now time.Time) ([]Sample, Observation, error) {
	if err := r.P.Tick(ctx); err != nil {
		return nil, Observation{}, err
	}
	snap, err := r.P.ForecastAt(ctx, lat, lon)
	if err != nil {
		return nil, Observation{}, err
	}
	var out []Sample
	var obs Observation
	for _, pt := range snap.Series {
		v := snap.FrameTime.Add(time.Duration(pt.Minute) * time.Minute)
		if pt.Minute == 0 {
			// Minute 0 is the echo itself: neither trend nor ML touch it.
			obs = Observation{Frame: v, DBZ: pt.DBZ}
		}
		if v.Before(now) || v.After(now.Add(Horizon)) || v.Unix()%int64(Step/time.Second) != 0 {
			continue
		}
		out = append(out, Sample{v, radar.RainRate(pt.DBZ), KindRadar})
	}
	return out, obs, nil
}

// Observation is the radar echo at a point in a frame.
type Observation struct {
	Frame time.Time
	DBZ   float32
}
