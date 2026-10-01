package verify

import (
	"math"
	"testing"
)

func approx(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestScores(t *testing.T) {
	var s Scores
	// 3 hits, 1 miss, 2 false alarms, 4 correct negatives.
	for range 3 {
		s.Add(true, true)
	}
	s.Add(false, true)
	s.Add(true, false)
	s.Add(true, false)
	for range 4 {
		s.Add(false, false)
	}
	s.Compute()
	approx(t, "accuracy", s.Accuracy, 0.7)
	approx(t, "pod", s.POD, 0.75)
	approx(t, "far", s.FAR, 0.4)
	approx(t, "csi", s.CSI, 0.5)
}

func TestScoresUndefined(t *testing.T) {
	var s Scores
	s.Add(false, false)
	s.Compute()
	if s.POD != nil || s.FAR != nil || s.CSI != nil {
		t.Fatalf("expected nil scores, got %+v", s)
	}
	approx(t, "accuracy", s.Accuracy, 1)
}

func TestBuildGroupsByLead(t *testing.T) {
	rows := []Row{
		{LeadMin: 20, PredRain: true, ObsRain: true, PersistRain: false, PredDBZ: 30, ObsDBZ: 25},
		{LeadMin: 10, PredRain: false, ObsRain: false, PersistRain: false},
		{LeadMin: 10, PredRain: true, ObsRain: false, PersistRain: true, PredDBZ: 22, ObsDBZ: -10},
	}
	r := Build(rows)
	if len(r.Leads) != 2 || r.Leads[0].LeadMin != 10 || r.Leads[1].LeadMin != 20 {
		t.Fatalf("leads = %+v", r.Leads)
	}
	if r.Model.N != 3 || r.Model.Hits != 1 || r.Model.FalseAlarms != 1 {
		t.Fatalf("model = %+v", r.Model)
	}
	approx(t, "persistence pod", r.Persistence.POD, 0)
	approx(t, "lead20 mae", r.Leads[1].MAEdBZ, 5)
	approx(t, "lead10 mae", r.Leads[0].MAEdBZ, 22)
}

func TestArrival(t *testing.T) {
	const t0 = 1_000_200
	obs := map[int64]bool{
		t0 + 600: false, t0 + 1200: false, t0 + 1800: true, // rain at +30
		t0 + 2400: true,
	}
	issues := []Issue{
		{IssuedAt: t0, ArrivalMin: 22},       // error +8
		{IssuedAt: t0 + 600, ArrivalMin: 30}, // actual +20 → error -10
		{IssuedAt: t0, ArrivalMin: -1},       // no prediction: skipped
		{IssuedAt: t0, RainingNow: true},     // skipped
	}
	st := Arrival(issues, obs, 10, 60)
	if st.N != 2 {
		t.Fatalf("n = %d", st.N)
	}
	approx(t, "mae", st.MAEMin, 9)
	approx(t, "bias", st.BiasMin, -1)
}

func TestPool(t *testing.T) {
	m1, b1, m2, b2 := 10.0, 10.0, 4.0, -4.0
	st := Pool([]ArrivalStat{{N: 1, MAEMin: &m1, BiasMin: &b1}, {N: 3, MAEMin: &m2, BiasMin: &b2}, {}})
	if st.N != 4 {
		t.Fatalf("n = %d", st.N)
	}
	approx(t, "mae", st.MAEMin, 5.5)
	approx(t, "bias", st.BiasMin, -0.5)
}

func TestShadowComparisonUsesPairedRowsOnly(t *testing.T) {
	rows := []Row{
		// Old row without a trend: counted in the main report only.
		{LeadMin: 10, PredRain: true, ObsRain: false},
		// Trend catches the rain the model missed.
		{LeadMin: 10, PredRain: false, ObsRain: true, Trend: &Prediction{DBZ: 25, Rain: true}},
		{LeadMin: 20, PredRain: true, ObsRain: true, Trend: &Prediction{DBZ: 30, Rain: true}},
	}
	r := Build(rows)
	if r.Model.N != 3 || r.Shadow == nil || r.Shadow.Model.N != 2 {
		t.Fatalf("model n=%d shadow=%+v", r.Model.N, r.Shadow)
	}
	approx(t, "model csi", r.Shadow.Model.CSI, 0.5)
	approx(t, "trend csi", r.Shadow.Trend.CSI, 1)
	if len(r.Shadow.Leads) != 2 || r.Shadow.Leads[0].LeadMin != 10 {
		t.Fatalf("leads = %+v", r.Shadow.Leads)
	}
	if Build(rows[:1]).Shadow != nil {
		t.Fatal("no trend rows should give a nil comparison")
	}
}
