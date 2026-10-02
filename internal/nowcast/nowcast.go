// Package nowcast extrapolates the latest radar frame along a motion field to
// predict when rain reaches a point.
package nowcast

import (
	"math"

	"raincast/internal/motion"
	"raincast/internal/radar"
)

// Options controls the extrapolation.
type Options struct {
	Horizon   int     // minutes ahead
	Threshold float32 // dBZ counted as rain
	Heavy     float32 // dBZ counted as heavy rain
	Radius    int     // pixels around the target; the median echo of this disc is used
	KmPerPx   float64 // ground resolution, for speed reporting
	// Trend, when set, lets echoes strengthen or weaken as they travel.
	// Its effect saturates: after m minutes the change is
	// rate·τ·(1−e^(−m/τ)), so a trend never runs away over the hour.
	Trend    *motion.Trend
	TrendTau float64 // minutes; default 20
	// Accel, when set, lets motion keep changing as it did lately (pixels
	// per minute²), damped the same way: after s minutes the velocity has
	// changed by A·τ·(1−e^(−s/τ)), at most A·τ.
	Accel    *motion.Field
	AccelTau float64 // minutes; default 20
	// ProbRadius, when set, also yields a rain probability per minute: the
	// share of pixels at or above Threshold within ProbRadius(m) pixels of
	// the upstream point. The radius grows with lead time because position
	// errors do.
	ProbRadius func(minute int) int
}

// DefaultProbRadius widens from 2 px (~2.5 km) now to 10 px (~12 km) at an
// hour.
func DefaultProbRadius(m int) int { return 2 + m*8/60 }

// trendCeiling caps grown echoes at a realistic reflectivity.
const trendCeiling = 60

// Point is the predicted echo at the target after Minute minutes.
type Point struct {
	Minute int     `json:"minute"`
	DBZ    float32 `json:"dbz"`
	// Prob is the chance of rain, when Options.ProbRadius is set.
	Prob float32 `json:"prob,omitempty"`
}

// Result is a forecast for one target point.
type Result struct {
	Series     []Point `json:"series"`
	RainingNow bool    `json:"raining_now"`
	// ArrivalMin is the first minute with rain, or -1 if none within Horizon.
	ArrivalMin int `json:"arrival_min"`
	// HeavyNow and HeavyArrivalMin are the same for heavy rain.
	HeavyNow        bool `json:"heavy_now"`
	HeavyArrivalMin int  `json:"heavy_arrival_min"`
	// SpeedKmh and DirectionDeg describe echo motion at the target; the
	// direction is where rain is heading, clockwise from north.
	SpeedKmh       float64 `json:"speed_kmh"`
	DirectionDeg   float64 `json:"direction_deg"`
	MotionReliable bool    `json:"motion_reliable"`
	// AccumMM is the rain expected over the horizon, in mm, from the
	// series through radar.RainRate.
	AccumMM float64 `json:"accum_mm"`
}

// ProbAt returns the rain probability at minute m (clamped to the series).
func (r Result) ProbAt(m int) float32 {
	if len(r.Series) == 0 {
		return 0
	}
	m = max(0, min(m, len(r.Series)-1))
	return r.Series[m].Prob
}

// At returns the predicted dBZ at minute m (clamped to the series).
func (r Result) At(m int) float32 {
	if len(r.Series) == 0 {
		return radar.MinDBZ
	}
	m = max(0, min(m, len(r.Series)-1))
	return r.Series[m].DBZ
}

// Forecast traces backward from target (x, y) through field f: the echo that
// will be over the target in m minutes is the one now at the upstream point
// (semi-Lagrangian advection, assuming steady motion and no growth/decay
// unless opt.Accel and opt.Trend say otherwise). Each one-minute step uses
// the velocity at its midpoint, so curved paths are followed closely.
func Forecast(g *radar.Grid, f *motion.Field, x, y float64, opt Options) Result {
	r := Result{Series: make([]Point, 0, opt.Horizon+1)}
	tau := opt.TrendTau
	if tau <= 0 {
		tau = 20
	}
	atau := opt.AccelTau
	if atau <= 0 {
		atau = 20
	}
	// accelShift is the extra displacement acceleration adds by minute s.
	accelShift := func(s float64) float64 { return atau*s - atau*atau*(1-math.Exp(-s/atau)) }
	vel := func(px, py, gain float64) motion.Vector {
		v := f.At(px, py)
		if opt.Accel != nil {
			a := opt.Accel.At(px, py)
			v.DX += a.DX * gain
			v.DY += a.DY * gain
		}
		return v
	}
	px, py := x, y
	for m := 0; m <= opt.Horizon; m++ {
		v := g.MedianInRadius(px, py, opt.Radius)
		// Only existing echoes change; empty sky does not grow rain.
		var delta float64
		if opt.Trend != nil && m > 0 {
			delta = opt.Trend.At(px, py) * tau * (1 - math.Exp(-float64(m)/tau))
		}
		if delta != 0 && v >= 10 {
			v = float32(math.Min(float64(v)+delta, trendCeiling))
		}
		pt := Point{Minute: m, DBZ: v}
		if opt.ProbRadius != nil {
			if rad := opt.ProbRadius(m); rad >= 0 {
				pt.Prob = rainShare(g, px, py, rad, opt.Threshold, float32(delta))
			}
		}
		r.Series = append(r.Series, pt)
		if f != nil {
			// Steady motion moves an echo the same distance every minute;
			// acceleration adds accelShift(m+1)−accelShift(m) for this one.
			// Along the backward path the minutes are taken nearest the
			// target first, which only matters where acceleration varies
			// over the distance travelled.
			gain := accelShift(float64(m+1)) - accelShift(float64(m))
			d := vel(px, py, gain)
			d = vel(px-d.DX/2, py-d.DY/2, gain)
			px -= d.DX
			py -= d.DY
		}
	}
	r = Summarize(r.Series, opt)

	if f != nil {
		r.MotionReliable = f.Reliable()
		v := f.At(x, y)
		r.SpeedKmh = math.Hypot(v.DX, v.DY) * opt.KmPerPx * 60
		// Pixel Y grows southward, so north is -DY.
		r.DirectionDeg = math.Mod(math.Atan2(v.DX, -v.DY)*180/math.Pi+360, 360)
	}
	return r
}

// Summarize fills a result's arrival times from its series: the first
// minute at or above opt.Threshold (rain) and opt.Heavy (heavy rain); and
// the accumulated rain, each minute after the first adding a minute of it.
func Summarize(series []Point, opt Options) Result {
	r := Result{Series: series, ArrivalMin: -1, HeavyArrivalMin: -1}
	for i, pt := range series {
		if i > 0 {
			r.AccumMM += radar.RainRate(pt.DBZ) / 60
		}
		if pt.DBZ >= opt.Threshold && r.ArrivalMin < 0 {
			r.ArrivalMin = pt.Minute
		}
		if opt.Heavy > 0 && pt.DBZ >= opt.Heavy && r.HeavyArrivalMin < 0 {
			r.HeavyArrivalMin = pt.Minute
		}
	}
	r.RainingNow = r.ArrivalMin == 0
	r.HeavyNow = r.HeavyArrivalMin == 0
	return r
}

// rainShare is the share of pixels within r of (x, y) at or above thr after
// adding delta to existing echoes.
func rainShare(g *radar.Grid, x, y float64, r int, thr, delta float32) float32 {
	cx, cy := int(math.Round(x)), int(math.Round(y))
	var n, rain int
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy > r*r {
				continue
			}
			n++
			v := g.At(cx+dx, cy+dy)
			if v >= 10 {
				v = min(v+delta, trendCeiling)
			}
			if v >= thr {
				rain++
			}
		}
	}
	return float32(rain) / float32(n)
}
