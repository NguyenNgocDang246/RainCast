package pipeline

import (
	"context"
	"encoding/json"
	"testing"

	"raincast/internal/ml"
)

// rainBundle forecasts rain (≥ 20 dBZ) everywhere at every lead: one class
// whose only tree is a leaf worth +5 (P ≈ 0.99).
func rainBundle(t *testing.T) func() *ml.Bundle {
	t.Helper()
	leaf := ml.Tree{Feature: []int{-1}, Threshold: []float64{0}, Left: []int{0}, Right: []int{0},
		DefaultLeft: []bool{false}, Missing: []uint8{0}, Value: []float64{5}}
	data, err := json.Marshal(ml.Bundle{Set: ml.SetRadar, Fold: -1, Features: []string{"m_mean"},
		Classes: []ml.Class{{DBZ: 20, Trees: []ml.Tree{leaf}, PStar: map[int]float64{10: 0.5}}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ml.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return func() *ml.Bundle { return b }
}

func withML(t *testing.T, p *Pipeline) *Pipeline {
	p.cfg.ML = rainBundle(t)
	return p
}

// The model adjusts the trended forecast at its leads and leaves now alone.
func TestMLAdjustsForecast(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	plain, err := testPipeline(t, rv, nil).ForecastAt(ctx, testLat, testLon)
	if err != nil {
		t.Fatal(err)
	}
	got, err := withML(t, testPipeline(t, rv, nil)).ForecastAt(ctx, testLat, testLon)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "ensemble+ml" || plain.Model != "ensemble" {
		t.Fatalf("models %q and %q", got.Model, plain.Model)
	}
	if got.Series[0] != plain.Series[0] {
		t.Errorf("minute 0 changed: %+v, was %+v", got.Series[0], plain.Series[0])
	}
	for _, l := range ml.Leads {
		if got.Series[l].DBZ < 20 {
			t.Errorf("minute %d = %v dBZ, the model forecasts rain", l, got.Series[l].DBZ)
		}
	}
	if got.ArrivalMin < 0 || got.ArrivalMin > 10 {
		t.Errorf("arrival %d not recomputed from the adjusted series", got.ArrivalMin)
	}
}

// Without the trend the forecasts are not the ones the model learned from.
func TestMLNeedsTrend(t *testing.T) {
	rv := newFakeRainViewer(t)
	p := withML(t, testPipeline(t, rv, nil))
	p.cfg.Model.Trend = false
	got, err := p.ForecastAt(context.Background(), testLat, testLon)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "ensemble" {
		t.Errorf("model %q without trend", got.Model)
	}
}

// A region from the shared cache carries its scene, frame before included,
// so another process forecasts the same.
func TestMLSceneShared(t *testing.T) {
	rv := newFakeRainViewer(t)
	ctx := context.Background()
	shared := &mapCache{m: map[string][]byte{}}
	p1 := withML(t, testPipeline(t, rv, shared))
	want, err := p1.ForecastAt(ctx, testLat, testLon)
	if err != nil {
		t.Fatal(err)
	}
	p2 := withML(t, testPipeline(t, rv, shared))
	got, err := p2.ForecastAt(ctx, testLat, testLon)
	if err != nil {
		t.Fatal(err)
	}
	if !sameForecast(got, want) || got.Model != want.Model {
		t.Fatalf("shared region forecasts %+v, want %+v", got.Result, want.Result)
	}
	for key, r := range p2.regions {
		if r.scene == nil || r.scene.Prev == nil {
			t.Errorf("region %s lost its scene or frame before", key)
		}
	}
	if len(p2.regions) == 0 {
		t.Error("no region from the shared cache")
	}
}
