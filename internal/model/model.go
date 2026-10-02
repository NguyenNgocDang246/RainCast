package model

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"raincast/internal/cell"
	"raincast/internal/motion"
	"raincast/internal/nowcast"
	"raincast/internal/radar"
)

// Member is one motion method in a model, with its share of the forecast.
type Member struct {
	Method string
	Pairs  int     // frame pairs its motion averages
	Weight float64 // relative; weights are normalized over usable members
	// Accel lets the member's motion keep changing as it did over its
	// frame pairs (damped), instead of staying as it is now.
	Accel bool
}

// Model forecasts by extrapolating the radar along each member's motion and
// averaging the members' echo and rain probability.
type Model struct {
	Name    string
	Members []Member
	// Trend lets echoes grow or weaken as they travel, each member along
	// its own motion.
	Trend bool
	// Storm, with Trend, follows convective cells through the frames along
	// each member's motion and sets the trend over each cell from its
	// life: growing or decaying, newly formed, merged or split.
	Storm bool
}

// Default is the model the app forecasts with: the equal mean of
// Lucas–Kanade, Horn–Schunck and TREC over 4 frame pairs, with the
// intensity trend. On 1,628 forecast times from 50 regions and 39 rain
// events (October 2026), the mean without trend beat each member alone,
// Lucas–Kanade by 0.8 CSI points and TREC by 1.5, in every climate group,
// and learned weights did no better than equal ones; adding the trend
// lifted the broader seven-method mean (since trimmed to these three) by
// another 0.8. Re-run
// cmd/backtest -fresh as data accumulates to check it still leads.
func Default() Model {
	return Model{Name: "ensemble", Trend: true, Members: []Member{
		{Method: LK, Pairs: 4, Weight: 1},
		{Method: HS, Pairs: 4, Weight: 1},
		{Method: TREC, Pairs: 4, Weight: 1},
	}}
}

// Single is a model of one method.
func Single(method string, pairs int, trend bool) Model {
	return Model{Name: method, Trend: trend, Members: []Member{{Method: method, Pairs: pairs, Weight: 1}}}
}

// Parse returns Default for "ensemble" and a single-method model for a
// method name, at the given frame pairs and with or without the trend.
func Parse(name string, pairs int, trend bool) (Model, error) {
	if name == "" || name == "ensemble" {
		m := Default()
		m.Trend = trend
		for i := range m.Members {
			m.Members[i].Pairs = pairs
		}
		return m, nil
	}
	for _, method := range Methods {
		if name == method {
			return Single(method, pairs, trend), nil
		}
	}
	return Model{}, fmt.Errorf("model: unknown model %q (ensemble or one of %s)", name, strings.Join(Methods, ", "))
}

// Prepared is a model's motion (and intensity trend) for one frame, ready
// to forecast any point of it.
type Prepared struct {
	fields  []*motion.Field
	trends  []*motion.Trend
	accels  []*motion.Field // nil for members without acceleration
	weights []float64
	// Storms are the cells the first usable member followed, for reporting
	// those near a point; nil unless the model uses Storm.
	Storms []StormInfo
	// Used is the most frames any member's motion used.
	Used int
	// Display is the members' mean motion, for drawing.
	Display *motion.Field
}

// Prepare estimates every member's motion at frame t, concurrently, and
// its intensity trend (rainDBZ outlines echoes for the trend). It returns
// nil when no member can see motion (no earlier frame).
func (m Model) Prepare(b *Builder, t int64, rainDBZ float32) *Prepared {
	n := len(m.Members)
	fields := make([]*motion.Field, n)
	trends := make([]*motion.Trend, n)
	accels := make([]*motion.Field, n)
	storms := make([][]cell.Storm, n)
	used := make([]int, n)
	var wg sync.WaitGroup
	for i, mem := range m.Members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fields[i], used[i] = b.Field(mem.Method, t, mem.Pairs)
			trends[i] = b.Trend(t, fields[i], rainDBZ)
			if m.Storm && trends[i] != nil {
				storms[i] = b.Storms(t, fields[i], mem.Pairs)
				trends[i] = cell.AdjustTrend(trends[i], storms[i], b.Grid(t).W)
			}
			if mem.Accel {
				accels[i] = b.Accel(mem.Method, t, mem.Pairs)
			}
		}()
	}
	wg.Wait()
	p := &Prepared{}
	var sum float64
	for i, f := range fields {
		if f == nil {
			continue
		}
		p.fields = append(p.fields, f)
		p.trends = append(p.trends, trends[i])
		p.accels = append(p.accels, accels[i])
		if m.Storm && p.Storms == nil {
			p.Storms = stormInfo(storms[i])
		}
		p.weights = append(p.weights, m.Members[i].Weight)
		sum += m.Members[i].Weight
		p.Used = max(p.Used, used[i])
	}
	if len(p.fields) == 0 || sum <= 0 {
		return nil
	}
	for i := range p.weights {
		p.weights[i] /= sum
	}
	p.Display = mean(p.fields, p.weights)
	return p
}

// HasTrend reports whether every member could measure an intensity trend.
func (p *Prepared) HasTrend() bool {
	for _, t := range p.trends {
		if t == nil {
			return false
		}
	}
	return true
}

// Forecast is the weighted mean of the members' nowcasts at (x, y): echo,
// rain probability, arrival times and motion. With trend, each member also
// grows or weakens echoes along its own motion (opt.Trend is ignored), and
// members with Accel speed up or slow down (opt.Accel is ignored).
func (p *Prepared) Forecast(g *radar.Grid, x, y float64, opt nowcast.Options, trend bool) nowcast.Result {
	var series []nowcast.Point
	var vx, vy float64
	reliable := false
	for i, f := range p.fields {
		o := opt
		o.Trend = nil
		if trend {
			o.Trend = p.trends[i]
		}
		o.Accel = p.accels[i]
		r := nowcast.Forecast(g, f, x, y, o)
		w := p.weights[i]
		if series == nil {
			series = make([]nowcast.Point, len(r.Series))
			for m := range series {
				series[m].Minute = r.Series[m].Minute
			}
		}
		for m := range series {
			series[m].DBZ += float32(w) * r.Series[m].DBZ
			series[m].Prob += float32(w) * r.Series[m].Prob
		}
		v := f.At(x, y)
		vx += w * v.DX
		vy += w * v.DY
		reliable = reliable || r.MotionReliable
	}
	res := nowcast.Summarize(series, opt)
	res.MotionReliable = reliable
	res.SpeedKmh = math.Hypot(vx, vy) * opt.KmPerPx * 60
	// Pixel Y grows southward, so north is -DY.
	res.DirectionDeg = math.Mod(math.Atan2(vx, -vy)*180/math.Pi+360, 360)
	return res
}

// mean averages fields onto the first one's block grid.
func mean(fields []*motion.Field, weights []float64) *motion.Field {
	if len(fields) == 1 {
		return fields[0]
	}
	base := fields[0]
	out := &motion.Field{BlockSize: base.BlockSize, BW: base.BW, BH: base.BH,
		V: make([]motion.Vector, len(base.V)), Valid: make([]bool, len(base.V))}
	bs := float64(base.BlockSize)
	var gx, gy float64
	for by := range base.BH {
		for bx := range base.BW {
			cx, cy := (float64(bx)+0.5)*bs, (float64(by)+0.5)*bs
			i := by*base.BW + bx
			for k, f := range fields {
				v := f.At(cx, cy)
				out.V[i].DX += weights[k] * v.DX
				out.V[i].DY += weights[k] * v.DY
				out.Valid[i] = out.Valid[i] || f.Reliable()
			}
			if out.Valid[i] {
				out.NValid++
			}
		}
	}
	for k, f := range fields {
		gx += weights[k] * f.Global.DX
		gy += weights[k] * f.Global.DY
	}
	out.Global = motion.Vector{DX: gx, DY: gy}
	return out
}

// StormInfo is what is reported about a followed cell.
type StormInfo struct {
	X, Y     float64 // centroid, pixels
	Peak     float32 // dBZ
	RateDBZ  float64 // dBZ/min
	New      bool
	Merged   bool
	Split    bool
	Decaying bool
}

func stormInfo(storms []cell.Storm) []StormInfo {
	out := make([]StormInfo, 0, len(storms))
	for _, s := range storms {
		out = append(out, StormInfo{X: s.X, Y: s.Y, Peak: s.Peak, RateDBZ: s.RateDBZ,
			New: s.New, Merged: s.Merged, Split: s.Split, Decaying: s.Decaying})
	}
	return out
}

// growingRate is the dBZ/min above which a followed cell counts as building.
const growingRate = 0.2

// Nearby counts, within r pixels of (x, y), the cells that are building:
// newly formed or merged, or strengthening by growingRate or more.
func (p *Prepared) Nearby(x, y, r float64) (building int) {
	for _, s := range p.Storms {
		if math.Hypot(s.X-x, s.Y-y) > r || s.Decaying {
			continue
		}
		if s.New || s.Merged || s.RateDBZ >= growingRate {
			building++
		}
	}
	return building
}
