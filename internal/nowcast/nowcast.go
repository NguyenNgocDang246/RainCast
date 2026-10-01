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
}

// trendCeiling caps grown echoes at a realistic reflectivity.
const trendCeiling = 60

// Point is the predicted echo at the target after Minute minutes.
type Point struct {
	Minute int     `json:"minute"`
	DBZ    float32 `json:"dbz"`
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
// (semi-Lagrangian advection, assuming steady motion and no growth/decay).
func Forecast(g *radar.Grid, f *motion.Field, x, y float64, opt Options) Result {
	r := Result{ArrivalMin: -1, HeavyArrivalMin: -1, Series: make([]Point, 0, opt.Horizon+1)}
	tau := opt.TrendTau
	if tau <= 0 {
		tau = 20
	}
	px, py := x, y
	for m := 0; m <= opt.Horizon; m++ {
		v := g.MedianInRadius(px, py, opt.Radius)
		// Only existing echoes change; empty sky does not grow rain.
		if opt.Trend != nil && m > 0 && v >= 10 {
			eff := tau * (1 - math.Exp(-float64(m)/tau))
			v = float32(math.Min(float64(v)+opt.Trend.At(px, py)*eff, trendCeiling))
		}
		r.Series = append(r.Series, Point{Minute: m, DBZ: v})
		if v >= opt.Threshold && r.ArrivalMin < 0 {
			r.ArrivalMin = m
		}
		if opt.Heavy > 0 && v >= opt.Heavy && r.HeavyArrivalMin < 0 {
			r.HeavyArrivalMin = m
		}
		if f != nil {
			d := f.At(px, py)
			px -= d.DX
			py -= d.DY
		}
	}
	r.RainingNow = r.ArrivalMin == 0
	r.HeavyNow = r.HeavyArrivalMin == 0

	if f != nil {
		r.MotionReliable = f.Reliable()
		v := f.At(x, y)
		r.SpeedKmh = math.Hypot(v.DX, v.DY) * opt.KmPerPx * 60
		// Pixel Y grows southward, so north is -DY.
		r.DirectionDeg = math.Mod(math.Atan2(v.DX, -v.DY)*180/math.Pi+360, 360)
	}
	return r
}
