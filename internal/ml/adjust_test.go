package ml

import (
	"math"
	"testing"

	"raincast/internal/motion"
	"raincast/internal/nowcast"
	"raincast/internal/radar"
)

func TestLeadDelta(t *testing.T) {
	delta := []float64{2, 4, 4, 0, -2, -6}
	for _, tc := range []struct {
		m    int
		want float64
	}{
		{0, 0}, {5, 1}, {10, 2}, {15, 3}, {20, 4}, {35, 2}, {60, -6}, {75, -6},
	} {
		if got := leadDelta(delta, tc.m); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("leadDelta(%d) = %v, want %v", tc.m, got, tc.want)
		}
	}
}

func TestLeadProbRadius(t *testing.T) {
	for m := 0; m <= 70; m++ {
		got := LeadProbRadius(m)
		lead := m > 0 && m%10 == 0 && m <= 60
		if lead && got != nowcast.DefaultProbRadius(m) || !lead && got != -1 {
			t.Errorf("LeadProbRadius(%d) = %d", m, got)
		}
	}
}

// constant is a member nowcast of dbz at every minute.
func constant(dbz float32) nowcast.Result {
	s := make([]nowcast.Point, 61)
	for m := range s {
		s[m] = nowcast.Point{Minute: m, DBZ: dbz}
	}
	return nowcast.Result{Series: s}
}

// Adjust moves the series to what Apply gives at the leads and
// interpolates between them, from no change now.
func TestAdjust(t *testing.T) {
	b := bundle(t)
	g := radar.NewGrid(256, 256)
	f := eastward(256, 256)
	s := &Scene{Grid: g, Fields: []*motion.Field{f, f, f}, Trends: make([]*motion.Trend, 3),
		KmPerPx: 1.2, Time: 1791000000, Lat: 10.8, Lon: 106.7}
	s.Prime()
	members := []nowcast.Result{constant(25), constant(25), constant(25)}
	series := constant(25).Series

	if !b.Adjust(s, 128, 128, members, series) {
		t.Fatal("not applied")
	}
	if series[0].DBZ != 25 {
		t.Errorf("minute 0 changed to %v", series[0].DBZ)
	}
	for _, l := range Leads {
		want := b.Apply(25, l, b.Predict(row(25)))
		if math.Abs(float64(series[l].DBZ-want)) > 1e-4 {
			t.Errorf("minute %d = %v, want Apply's %v", l, series[l].DBZ, want)
		}
	}
	if want := 25 + (series[10].DBZ-25)/2; math.Abs(float64(series[5].DBZ-want)) > 1e-4 {
		t.Errorf("minute 5 = %v, want halfway %v", series[5].DBZ, want)
	}

	if b.Adjust(s, 128, 128, make([]nowcast.Result, 3), constant(25).Series) {
		t.Error("applied without members")
	}
}
