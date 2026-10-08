// Package ml post-processes the extrapolation ensemble with gradient-boosted
// trees: from the members' forecasts and what extrapolation cannot see —
// the storms' lives, the time of day, the convective environment (NWP) and
// cloud tops (Himawari) — it predicts the chance that rain at a point will
// reach each strength at each lead. The trees are trained offline
// (scripts/ml/train.py) on rows the backtest dumps, and only evaluated here.
//
// It never regresses dBZ: averaging echoes that ended up displaced teaches
// "strong echoes weaken", which wipes out heavy rain. The member mean is
// only moved into the strength band the probabilities point to (Apply).
package ml

import (
	"math"
	"sort"

	"raincast/internal/cell"
	"raincast/internal/himawari"
	"raincast/internal/motion"
	"raincast/internal/nowcast"
	"raincast/internal/nwp"
	"raincast/internal/radar"
)

// Members are the motion methods whose forecasts are features, in order.
var Members = []string{"lk", "hs", "trec"}

// Names lists every feature in row order. Prefixes group them: "nwp_" and
// "sat_" can be left out of a model (feature sets).
var Names = []string{
	"lead",
	"m_lk", "m_hs", "m_trec", "m_mean", "m_std", "m_min", "m_max", "m_prob",
	"now_dbz", "near10_frac", "near10_max", "near25_frac", "near25_max",
	"up_frac", "up_max", "up_mean", "up_trend", "up_dist_km",
	"speed_kmh", "coherence", "gain",
	"storm_dist_km", "storm_peak", "storm_rate", "storm_age", "storm_area_km2", "storm_new", "storm_merged", "storm_decaying",
	"storms_building25",
	"hour_sin", "hour_cos", "abs_lat",
	"nwp_cape", "nwp_cin", "nwp_li", "nwp_precip", "nwp_showers", "nwp_cloud", "nwp_tcwv",
	"sat_now", "sat_up", "sat_up_max", "sat_up_cooling", "sat_up_cold", "sat_age_min",
}

var nameIndex = func() map[string]int {
	m := map[string]int{}
	for i, n := range Names {
		m[n] = i
	}
	return m
}()

// Index returns a feature's position in Names, or -1.
func Index(name string) int {
	if i, ok := nameIndex[name]; ok {
		return i
	}
	return -1
}

const (
	rainDBZ = 20
	// coldGray outlines cold, high cloud tops on the tile scale (about the
	// deep convection the tiles show white).
	coldGray = 200
	// gainRadius matches the model package's motion check (~30 km).
	gainRadius = 24
)

// Scene is everything known at one issue time over one radar mosaic.
type Scene struct {
	Grid *radar.Grid
	// OriginX, OriginY are the global radar pixel of Grid's (0, 0), at
	// himawari.RadarZoom.
	OriginX, OriginY float64
	// Fields and Trends are the members' (Members order); a nil field
	// means the member is missing, a nil trend no trend.
	Fields []*motion.Field
	Trends []*motion.Trend
	Storms []cell.Storm
	// Prev is the frame PrevMinutes before, for the motion check; nil
	// leaves the gain unknown.
	Prev        *radar.Grid
	PrevMinutes float64
	KmPerPx     float64
	Time        int64 // unix s
	Lat, Lon    float64
	NWP         *nwp.Series     // nil: unknown
	Sat         *himawari.Frame // nil: unknown
	// SatPast is a scan about 30 minutes before Sat, for cooling.
	SatPast *himawari.Frame

	mean *motion.Field
}

// Point holds what does not depend on lead, for one target pixel.
type Point struct {
	X, Y  float64
	fixed []float32 // lead-independent features, NaN-filled template
	up    []upstream
}

type upstream struct{ x, y float64 }

// Prepare computes the lead-independent features of (x, y) and its
// upstream points at each lead in leads (minutes, ascending).
func (s *Scene) Prepare(x, y float64, leads []int) *Point {
	if s.mean == nil {
		s.mean = meanField(s.Fields)
	}
	p := &Point{X: x, Y: y, fixed: nanRow()}
	f := p.fixed
	g := s.Grid
	f[Index("now_dbz")] = g.At(int(math.Round(x)), int(math.Round(y)))
	r10 := max(1, int(math.Round(10/s.KmPerPx)))
	r25 := max(1, int(math.Round(25/s.KmPerPx)))
	f[Index("near10_frac")], f[Index("near10_max")], _ = disc(g, x, y, r10)
	f[Index("near25_frac")], f[Index("near25_max")], _ = disc(g, x, y, r25)

	if s.mean != nil {
		v := s.mean.At(x, y)
		kmh, _ := nowcast.Heading(v, s.KmPerPx)
		f[Index("speed_kmh")] = float32(kmh)
		var vx, vy, sp float64
		n := 0
		for _, fl := range s.Fields {
			if fl == nil {
				continue
			}
			u := fl.At(x, y)
			vx += u.DX
			vy += u.DY
			sp += math.Hypot(u.DX, u.DY)
			n++
		}
		if sp > 0 {
			f[Index("coherence")] = float32(math.Hypot(vx, vy) / sp)
		}
		if s.Prev != nil && s.PrevMinutes > 0 {
			if gain, ok := motion.TranslationGain(s.Prev, g, v, s.PrevMinutes, x, y, gainRadius); ok {
				f[Index("gain")] = float32(gain)
			}
		}
	}

	s.storm(f, x, y, r25)
	hour := float64(s.Time%86400)/3600 + s.Lon/15
	f[Index("hour_sin")] = float32(math.Sin(2 * math.Pi * hour / 24))
	f[Index("hour_cos")] = float32(math.Cos(2 * math.Pi * hour / 24))
	f[Index("abs_lat")] = float32(math.Abs(s.Lat))
	if s.Sat != nil {
		if v, ok := s.sat(s.Sat, x, y); ok {
			f[Index("sat_now")] = float32(v)
		}
		f[Index("sat_age_min")] = float32(s.Time-s.Sat.Scan) / 60
	}

	// Upstream points along the members' mean motion, one minute at a
	// time with the midpoint velocity, as nowcast.Forecast traces them.
	p.up = make([]upstream, len(leads))
	px, py := x, y
	m := 0
	for i, l := range leads {
		for ; m < l && s.mean != nil; m++ {
			d := s.mean.At(px, py)
			d = s.mean.At(px-d.DX/2, py-d.DY/2)
			px -= d.DX
			py -= d.DY
		}
		p.up[i] = upstream{px, py}
	}
	return p
}

// Row fills out (len(Names)) for p at leads[li], given the members'
// forecast echo (dBZ, Members order, NaN when missing) and their mean rain
// probability there.
func (s *Scene) Row(p *Point, li, lead int, members []float32, prob float32, out []float32) {
	copy(out, p.fixed)
	out[Index("lead")] = float32(lead)
	var vals []float64
	for k, v := range members {
		out[Index("m_"+Members[k])] = v
		if !math.IsNaN(float64(v)) {
			vals = append(vals, float64(v))
		}
	}
	if len(vals) > 0 {
		sort.Float64s(vals)
		var sum, sq float64
		for _, v := range vals {
			sum += v
		}
		mean := sum / float64(len(vals))
		for _, v := range vals {
			sq += (v - mean) * (v - mean)
		}
		out[Index("m_mean")] = float32(mean)
		out[Index("m_std")] = float32(math.Sqrt(sq / float64(len(vals))))
		out[Index("m_min")] = float32(vals[0])
		out[Index("m_max")] = float32(vals[len(vals)-1])
	}
	out[Index("m_prob")] = prob

	u := p.up[li]
	out[Index("up_dist_km")] = float32(math.Hypot(u.x-p.X, u.y-p.Y) * s.KmPerPx)
	frac, mx, mean := disc(s.Grid, u.x, u.y, 5)
	out[Index("up_frac")], out[Index("up_max")], out[Index("up_mean")] = frac, mx, mean
	var tr float64
	nt := 0
	for _, t := range s.Trends {
		if t != nil {
			tr += t.At(u.x, u.y)
			nt++
		}
	}
	if nt > 0 {
		out[Index("up_trend")] = float32(tr / float64(nt))
	}

	if s.NWP != nil {
		e := s.NWP.At(s.Time + int64(lead)*60)
		out[Index("nwp_cape")], out[Index("nwp_cin")], out[Index("nwp_li")] = e.CAPE, e.CIN, e.LI
		out[Index("nwp_precip")], out[Index("nwp_showers")] = e.Precip, e.Showers
		out[Index("nwp_cloud")], out[Index("nwp_tcwv")] = e.Cloud, e.TCWV
	}
	if s.Sat != nil {
		if v, ok := s.sat(s.Sat, u.x, u.y); ok {
			out[Index("sat_up")] = float32(v)
			if s.SatPast != nil {
				if w, ok := s.sat(s.SatPast, u.x, u.y); ok {
					out[Index("sat_up_cooling")] = float32(v - w) // brighter is colder
				}
			}
		}
		mx, cold, ok := s.satDisc(s.Sat, u.x, u.y, 5)
		if ok {
			out[Index("sat_up_max")], out[Index("sat_up_cold")] = mx, cold
		}
	}
}

// storm fills the features of the storm nearest (x, y) and counts storms
// building within r pixels.
func (s *Scene) storm(f []float32, x, y float64, r int) {
	best := -1
	bestD := math.Inf(1)
	building := 0
	for i, st := range s.Storms {
		d := math.Hypot(st.X-x, st.Y-y)
		if d < bestD {
			best, bestD = i, d
		}
		if d <= float64(r) && !st.Decaying && (st.New || st.Merged || st.RateDBZ >= 0.2) {
			building++
		}
	}
	f[Index("storms_building25")] = float32(building)
	if best < 0 {
		return
	}
	st := s.Storms[best]
	b := func(v bool) float32 {
		if v {
			return 1
		}
		return 0
	}
	f[Index("storm_dist_km")] = float32(bestD * s.KmPerPx)
	f[Index("storm_peak")] = st.Peak
	f[Index("storm_rate")] = float32(st.RateDBZ)
	f[Index("storm_age")] = float32(st.Age)
	f[Index("storm_area_km2")] = float32(float64(st.Area) * s.KmPerPx * s.KmPerPx)
	f[Index("storm_new")], f[Index("storm_merged")], f[Index("storm_decaying")] = b(st.New), b(st.Merged), b(st.Decaying)
}

func (s *Scene) satPx(x, y float64) (float64, float64) {
	return himawari.FromRadar(s.OriginX+x, s.OriginY+y)
}

func (s *Scene) sat(fr *himawari.Frame, x, y float64) (float64, bool) {
	sx, sy := s.satPx(x, y)
	return fr.At(sx, sy)
}

// satDisc is the brightest gray and the share at or above coldGray within
// r satellite pixels of radar pixel (x, y).
func (s *Scene) satDisc(fr *himawari.Frame, x, y float64, r int) (mx, cold float32, ok bool) {
	sx, sy := s.satPx(x, y)
	n, c := 0, 0
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy > r*r {
				continue
			}
			v, ok := fr.At(sx+float64(dx), sy+float64(dy))
			if !ok {
				continue
			}
			n++
			mx = max(mx, float32(v))
			if v >= coldGray {
				c++
			}
		}
	}
	if n == 0 {
		return 0, 0, false
	}
	return mx, float32(c) / float32(n), true
}

// disc is the share of rain, the strongest echo and the mean echo (as the
// dBZ of the mean rain rate) within r pixels of (x, y).
func disc(g *radar.Grid, x, y float64, r int) (frac, mx, mean float32) {
	cx, cy := int(math.Round(x)), int(math.Round(y))
	n, rain := 0, 0
	var rr float64
	mx = radar.MinDBZ
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy > r*r {
				continue
			}
			v := g.At(cx+dx, cy+dy)
			n++
			mx = max(mx, v)
			if v >= rainDBZ {
				rain++
			}
			rr += radar.RainRate(v)
		}
	}
	mean = radar.MinDBZ
	if rr > 0 {
		mean = radar.DBZ(rr / float64(n))
	}
	return float32(rain) / float32(n), mx, mean
}

func meanField(fields []*motion.Field) *motion.Field {
	var use []*motion.Field
	for _, f := range fields {
		if f != nil {
			use = append(use, f)
		}
	}
	switch len(use) {
	case 0:
		return nil
	case 1:
		return use[0]
	}
	// The methods' block grids differ: sample every field at the first
	// one's block centers, as model.Prepared's display mean does.
	base := use[0]
	v := make([]motion.Vector, len(base.V))
	valid := make([]bool, len(base.V))
	bs := float64(base.BlockSize)
	for by := range base.BH {
		for bx := range base.BW {
			cx, cy := (float64(bx)+0.5)*bs, (float64(by)+0.5)*bs
			i := by*base.BW + bx
			for _, f := range use {
				u := f.At(cx, cy)
				v[i].DX += u.DX / float64(len(use))
				v[i].DY += u.DY / float64(len(use))
			}
			valid[i] = true
		}
	}
	out := &motion.Field{BlockSize: base.BlockSize, BW: base.BW, BH: base.BH, V: v, Valid: valid, NValid: len(v)}
	for _, f := range use {
		out.Global.DX += f.Global.DX / float64(len(use))
		out.Global.DY += f.Global.DY / float64(len(use))
	}
	return out
}

func nanRow() []float32 {
	r := make([]float32, len(Names))
	for i := range r {
		r[i] = float32(math.NaN())
	}
	return r
}
