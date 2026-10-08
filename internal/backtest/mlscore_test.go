package backtest

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"raincast/internal/ml"
)

// echoBundle forecasts rain exactly where the members' mean says so: one
// stump per class on m_mean at the class's dBZ.
func echoBundle(t *testing.T) *ml.Bundle {
	stump := func(thr float64) map[string]any {
		return map[string]any{
			"feature": []int{0, -1, -1}, "threshold": []float64{thr - 0.001, 0, 0},
			"left": []int{1, 0, 0}, "right": []int{2, 0, 0},
			"default_left": []bool{true, false, false}, "missing": []int{2, 0, 0},
			"value": []float64{0, -6, 6},
		}
	}
	data, _ := json.Marshal(map[string]any{
		"set": ml.SetRadar, "fold": 0, "features": []string{"m_mean"},
		"classes": []map[string]any{
			{"dbz": 20, "trees": []any{stump(20)}, "pstar": map[string]float64{"10": 0.5}},
			{"dbz": 30, "trees": []any{stump(30)}, "pstar": map[string]float64{"10": 0.5}},
		},
	})
	b, err := ml.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMLVariantAndDump(t *testing.T) {
	var frames []Frame
	for i := range 14 {
		frames = append(frames, Frame{Time: int64(1791000000 + 600*i), Mosaic: moving(i, 6)})
	}
	cfg := Config{Leads: []int{10, 20}, Threshold: 20, Radius: 0, Step: 4, StepSec: 600}
	var trended []string
	for _, m := range ml.Members {
		n := "m-" + m
		trended = append(trended, n)
		cfg.Variants = append(cfg.Variants, Variant{Name: n, Method: m, Pairs: motionPairs, Trend: true})
	}
	cfg.Variants = append(cfg.Variants, Variant{Name: "mean", Method: MethodMean, Members: trended, Trend: true})
	b := echoBundle(t)
	path := filepath.Join(t.TempDir(), "rows.bin")
	w, err := ml.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ML = &MLSetup{Dump: w, DumpEvery: 2, Bundles: map[string][2]*ml.Bundle{ml.SetRadar: {b, b}}}
	AddMLVariants(&cfg, []string{ml.SetRadar})

	rep := Run(frames, cfg)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if rep.Issues == 0 {
		t.Fatal("no issues")
	}
	var mean, mlCSI *float64
	for _, r := range rep.Results {
		switch r.Name {
		case "mean":
			mean = r.Overall.CSI
		case "ML":
			mlCSI = r.Overall.CSI
		}
	}
	if mean == nil || mlCSI == nil {
		t.Fatalf("results %+v", rep.Results)
	}
	// A model echoing the member mean forecasts the same rain.
	if math.Abs(*mean-*mlCSI) > 0.02 {
		t.Fatalf("ML CSI %.3f, mean %.3f", *mlCSI, *mean)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n := len(ml.Leading) + len(ml.Names)
	if len(data) == 0 || len(data)%(4*n) != 0 {
		t.Fatalf("%d bytes is not whole rows of %d", len(data), n)
	}
	col := func(r, c int) float32 {
		return math.Float32frombits(binary.LittleEndian.Uint32(data[4*(r*n+c):]))
	}
	rows := len(data) / (4 * n)
	lead := ml.Index("lead") + len(ml.Leading)
	rain := 0
	for r := range rows {
		if col(r, 3) != col(r, lead) || (col(r, 3) != 10 && col(r, 3) != 20) {
			t.Fatalf("row %d: lead column %v vs feature %v", r, col(r, 3), col(r, lead))
		}
		if w := col(r, 4); w != 1 && w != 1/ml.DryKeep {
			t.Fatalf("row %d weight %v", r, w)
		}
		if col(r, 5) >= 20 {
			rain++
		}
	}
	if rain == 0 {
		t.Fatal("no rainy observations dumped")
	}
}

func TestProgressReportsEveryTimeOnce(t *testing.T) {
	var frames []Frame
	for i := range 10 {
		frames = append(frames, Frame{Time: int64(600 * (i + 1)), Mosaic: moving(i, 6)})
	}
	var calls int
	var done, total int64
	Run(frames, Config{
		Variants: []Variant{{Name: "m2", Pairs: 2}}, Leads: []int{10, 20},
		Threshold: 20, Radius: 1, Step: 4, StepSec: 600,
		Progress: func(d, n int64, _ time.Duration) { calls, done, total = calls+1, d, n },
	})
	if calls == 0 || total != 10 || done != total {
		t.Fatalf("%d calls, last %d/%d; want a final 10/10", calls, done, total)
	}
}

func TestSatelliteGroupsAndComparisons(t *testing.T) {
	var frames []Frame
	for i := range 10 {
		frames = append(frames, Frame{Time: int64(600 * (i + 1)), Mosaic: moving(i, 6)})
	}
	cfg := Config{
		Variants: []Variant{{Name: "m1", Pairs: 1}, {Name: "m2", Pairs: 2}}, Leads: []int{10, 20},
		Threshold: 20, Radius: 1, Step: 4, StepSec: 600,
		Compare: [][2]string{{"m1", "m2"}, {"m1", "missing"}},
	}
	regions := []Region{
		{Name: "hcm", Climate: "tropical", Lat: 10.8, Lon: 106.7, Source: memSource(frames)},
		{Name: "paris", Climate: "midlat", Lat: 48.9, Lon: 2.3, Source: memSource(frames)},
	}
	rep, err := RunRegions(context.Background(), regions, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Group{}
	for _, g := range rep.Groups {
		got[g.Climate] = g
	}
	for key, regions := range map[string]int{"tropical": 1, "midlat": 1, "sat": 1, "nosat": 1, "tropical_sat": 1} {
		if g, ok := got[key]; !ok || g.Regions != regions || g.Issues == 0 {
			t.Errorf("group %q: %+v", key, g)
		}
	}
	if len(rep.Comparisons) != 1 {
		t.Fatalf("comparisons %+v: the pair with a missing variant must be left out", rep.Comparisons)
	}
	c := rep.Comparisons[0]
	want := *rep.Results[2].Overall.CSI - *rep.Results[1].Overall.CSI // every lead is ≤ 60
	if c.Delta == nil || math.Abs(*c.Delta-want) > 1e-9 || c.MaxLead != 20 {
		t.Fatalf("comparison %+v, want delta %v", c, want)
	}
	if len(got["sat"].Comparisons) != 1 {
		t.Fatal("groups carry the comparisons too")
	}
}
