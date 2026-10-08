package ml

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"

	"raincast/internal/motion"
	"raincast/internal/radar"
)

var nan = math.NaN()

// stump splits feature 0 at 1: left (≤ 1) is worth -2, right +2; NaN goes
// by missing and defaultLeft.
func stump(missing uint8, defaultLeft bool) Tree {
	return Tree{
		Feature:     []int{0, -1, -1},
		Threshold:   []float64{1, 0, 0},
		Left:        []int{1, 0, 0},
		Right:       []int{2, 0, 0},
		DefaultLeft: []bool{defaultLeft, false, false},
		Missing:     []uint8{missing, 0, 0},
		Value:       []float64{0, -2, 2},
	}
}

func TestTreeMissingValues(t *testing.T) {
	for _, tc := range []struct {
		name    string
		missing uint8
		defLeft bool
		x       float64
		want    float64
	}{
		{"value left", 2, false, 0.5, -2},
		{"value right", 2, true, 3, 2},
		{"NaN default right", 2, false, nan, 2},
		{"NaN default left", 2, true, nan, -2},
		{"None: NaN is 0", 0, false, nan, -2},
		{"Zero: 0 goes default", 1, false, 0, 2},
		{"Zero: NaN goes default", 1, false, nan, 2},
		{"Zero: other values compare", 1, false, 0.5, -2},
	} {
		tr := stump(tc.missing, tc.defLeft)
		if got := tr.eval([]float64{tc.x}); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// bundle makes a two-class bundle over feature "m_mean": P20 from one
// stump at 20 dBZ, P30 from one at 25 (so P30 ≤ P20 must be enforced
// when the trees disagree).
func bundle(t *testing.T) *Bundle {
	split := func(thr float64) Tree {
		tr := stump(2, false)
		tr.Threshold[0] = thr
		return tr
	}
	raw := Bundle{
		Set: SetAll, Fold: -1, Features: []string{"m_mean"},
		Classes: []Class{
			{DBZ: 20, Trees: []Tree{split(20)}, PStar: map[int]float64{10: 0.5, 60: 0.6}},
			{DBZ: 30, Trees: []Tree{split(15)}, PStar: map[int]float64{10: 0.5}},
		},
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func row(mean float32) []float32 {
	r := nanRow()
	r[Index("m_mean")] = mean
	return r
}

func TestPredictNeverRisesWithStrength(t *testing.T) {
	b := bundle(t)
	// m_mean 18: P20 low (left of 20), P30 high (right of 15) → capped.
	ps := b.Predict(row(18))
	if ps[1] > ps[0] {
		t.Fatalf("P30 %v > P20 %v", ps[1], ps[0])
	}
	if ps := b.Predict(row(40)); ps[0] < 0.8 || ps[1] < 0.8 {
		t.Fatalf("strong echo: %v", ps)
	}
}

func TestApplyKeepsEchoInBand(t *testing.T) {
	b := bundle(t)
	hi, lo := []float64{0.9, 0.9}, []float64{0.1, 0.1}
	mid := []float64{0.9, 0.1}
	for _, tc := range []struct {
		e    float32
		ps   []float64
		want float32
	}{
		{45, hi, 45},     // strong core kept as is
		{25, hi, 30},     // lifted to the band P30 points to
		{25, mid, 25},    // inside [20, 30): kept
		{35, mid, 29.99}, // capped below the class not forecast
		{15, mid, 20},    // lifted to rain
		{25, lo, 19.99},  // rain not forecast: kept below 20
		{5, lo, 5},
	} {
		if got := b.Apply(tc.e, 10, tc.ps); math.Abs(float64(got-tc.want)) > 1e-4 {
			t.Errorf("Apply(%v, %v) = %v, want %v", tc.e, tc.ps, got, tc.want)
		}
	}
	// PStar at an unlearned lead comes from the nearest one: 60 → 0.6.
	if got := b.Apply(25, 70, []float64{0.55, 0}); got >= 20 {
		t.Fatalf("lead 70 should use PStar 0.6, got %v", got)
	}
}

func TestParseRejectsBadBundles(t *testing.T) {
	for name, js := range map[string]string{
		"unknown feature": `{"features":["nope"],"classes":[{"dbz":20,"trees":[]}]}`,
		"no classes":      `{"features":["lead"],"classes":[]}`,
		"short arrays":    `{"features":["lead"],"classes":[{"dbz":20,"trees":[{"feature":[0,-1,-1],"threshold":[1],"left":[1,0,0],"right":[2,0,0],"default_left":[false,false,false],"missing":[0,0,0],"value":[0,1,2]}]}]}`,
		"loop":            `{"features":["lead"],"classes":[{"dbz":20,"trees":[{"feature":[0,-1,-1],"threshold":[1,0,0],"left":[0,0,0],"right":[2,0,0],"default_left":[false,false,false],"missing":[0,0,0],"value":[0,1,2]}]}]}`,
	} {
		if _, err := Parse([]byte(js)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestKeepIsStableAndWeighted(t *testing.T) {
	if ok, w := Keep(false, 3, 1791000000, 30, 7); !ok || w != 1 {
		t.Fatal("active rows are always kept")
	}
	kept := 0
	for p := range 20000 {
		ok, w := Keep(true, 3, 1791000000, 30, p)
		ok2, _ := Keep(true, 3, 1791000000, 30, p)
		if ok != ok2 {
			t.Fatal("Keep must not change between runs")
		}
		if ok {
			kept++
			if w != 1/DryKeep {
				t.Fatalf("weight %v", w)
			}
		}
	}
	if share := float64(kept) / 20000; math.Abs(share-DryKeep) > 0.01 {
		t.Fatalf("kept %.3f of quiet rows, want ~%v", share, DryKeep)
	}
}

func TestWriterRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.bin")
	w, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	id := w.Region(RegionInfo{Name: "a", Climate: "tropical"})
	if w.Region(RegionInfo{Name: "a"}) != id || w.Region(RegionInfo{Name: "b"}) != id+1 {
		t.Fatal("region ids")
	}
	n := len(Leading) + len(Names)
	r1 := make([]float32, n)
	r2 := make([]float32, n)
	for i := range r1 {
		r1[i], r2[i] = float32(i), float32(-i)
	}
	r2[n-1] = float32(nan)
	w.Rows([][]float32{r1, r2})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 2*n*4 {
		t.Fatalf("%d bytes", len(data))
	}
	if v := math.Float32frombits(binary.LittleEndian.Uint32(data[4*(n+3):])); v != -3 {
		t.Fatalf("row 2 col 3 = %v", v)
	}
	if v := math.Float32frombits(binary.LittleEndian.Uint32(data[len(data)-4:])); !math.IsNaN(float64(v)) {
		t.Fatal("NaN lost")
	}
	var meta struct {
		Columns []string
		Rows    int
		Regions []RegionInfo
	}
	side, err := os.ReadFile(path + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(side, &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.Columns) != n || meta.Rows != 2 || len(meta.Regions) != 2 || meta.Regions[0].Name != "a" {
		t.Fatalf("meta %+v", meta)
	}
}

// eastward is a field moving everything 0.5 px/min east.
func eastward(w, h int) *motion.Field {
	const bs = 32
	bw, bh := w/bs, h/bs
	v := make([]motion.Vector, bw*bh)
	valid := make([]bool, bw*bh)
	for i := range v {
		v[i], valid[i] = motion.Vector{DX: 0.5}, true
	}
	return motion.FromBlocks(bs, bw, bh, v, valid)
}

func TestSceneFeatures(t *testing.T) {
	g := radar.NewGrid(256, 256)
	// Rain west of the target: what an eastward motion brings in.
	for y := 120; y < 136; y++ {
		for x := 100; x < 116; x++ {
			g.Set(x, y, 35)
		}
	}
	f := eastward(256, 256)
	s := &Scene{Grid: g, Fields: []*motion.Field{f, f, nil}, Trends: []*motion.Trend{nil, nil, nil},
		KmPerPx: 1.2, Time: 1791000000, Lat: 10.8, Lon: 106.7}
	leads := []int{10, 30}
	p := s.Prepare(128, 128, leads)
	out := make([]float32, len(Names))
	s.Row(p, 1, 30, []float32{35, 33, float32(nan)}, 0.8, out)

	get := func(n string) float32 { return out[Index(n)] }
	if get("lead") != 30 || get("m_mean") != 34 || get("m_max") != 35 || get("m_prob") != 0.8 {
		t.Fatalf("member features: lead %v mean %v max %v prob %v", get("lead"), get("m_mean"), get("m_max"), get("m_prob"))
	}
	if !math.IsNaN(float64(get("m_trec"))) {
		t.Fatal("missing member must stay NaN")
	}
	// 30 minutes upstream at 0.5 px/min is 15 px west (x = 113, in the
	// rain); at 10 minutes, 5 px.
	if d := get("up_dist_km"); math.Abs(float64(d)-15*1.2) > 0.5 {
		t.Fatalf("upstream distance %v km, want 18", d)
	}
	if p.up[0].x > 124 || p.up[0].x < 122 || p.up[1].x > 114 || p.up[1].x < 112 {
		t.Fatalf("upstream points %+v must lie west, against the motion", p.up)
	}
	if get("up_max") != 35 || get("up_frac") <= 0 {
		t.Fatalf("upstream rain: max %v frac %v", get("up_max"), get("up_frac"))
	}
	if get("now_dbz") >= 20 || get("near10_frac") != 0 || get("near25_frac") <= 0 || get("near25_max") != 35 {
		t.Fatalf("near: now %v frac %v max %v", get("now_dbz"), get("near25_frac"), get("near25_max"))
	}
	if kmh := get("speed_kmh"); math.Abs(float64(kmh)-0.5*1.2*60) > 0.5 {
		t.Fatalf("speed %v km/h, want 36", kmh)
	}
	if get("coherence") < 0.99 {
		t.Fatalf("coherence %v of identical fields", get("coherence"))
	}
	for _, n := range []string{"nwp_cape", "sat_now", "sat_up", "sat_up_cooling", "gain", "storm_dist_km", "up_trend"} {
		if !math.IsNaN(float64(get(n))) {
			t.Errorf("%s = %v without its source, want NaN", n, get(n))
		}
	}
	if get("storms_building25") != 0 {
		t.Error("no storms, none building")
	}
	if h := math.Hypot(float64(get("hour_sin")), float64(get("hour_cos"))); math.Abs(h-1) > 1e-5 {
		t.Errorf("hour on the unit circle: %v", h)
	}
}

// The methods' fields come on different block grids; their mean must not
// assume one.
func TestMeanFieldAcrossBlockSizes(t *testing.T) {
	a := eastward(256, 256) // 32 px blocks
	v := make([]motion.Vector, 16*16)
	valid := make([]bool, len(v))
	for i := range v {
		v[i], valid[i] = motion.Vector{DY: 1}, true
	}
	b := motion.FromBlocks(16, 16, 16, v, valid)
	m := meanField([]*motion.Field{a, nil, b})
	if u := m.At(128, 128); math.Abs(u.DX-0.25) > 1e-9 || math.Abs(u.DY-0.5) > 1e-9 {
		t.Fatalf("mean %+v, want (0.25, 0.5)", u)
	}
}

// The trained model must give the probabilities LightGBM gave the parity
// rows when it was trained (scripts/ml/train.py writes both).
func TestModelMatchesTraining(t *testing.T) {
	data, perr := os.ReadFile(filepath.Join("models", "parity.json"))
	if errors.Is(perr, fs.ErrNotExist) {
		t.Skip("no trained model yet")
	}
	if perr != nil {
		t.Fatal(perr)
	}
	var parity struct {
		Set   string
		Names []string
		Rows  [][]*float64
		Probs [][]float64
	}
	if err := json.Unmarshal(data, &parity); err != nil {
		t.Fatal(err)
	}
	b, err := Load(filepath.Join("models", parity.Set+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for r, vals := range parity.Rows {
		row := nanRow()
		for i, n := range parity.Names {
			if j := Index(n); j >= 0 && vals[i] != nil {
				row[j] = float32(*vals[i])
			}
		}
		got := b.Predict(row)
		for k := range got {
			if math.Abs(got[k]-parity.Probs[r][k]) > 1e-4 {
				t.Fatalf("row %d class %d: Go %v, LightGBM %v", r, k, got[k], parity.Probs[r][k])
			}
		}
	}
}

// TestFold pins the folds of a few centers: scripts/ml/train.py (FOLD_CHECK)
// recomputes folds by the same rule and checks the same table.
func TestFold(t *testing.T) {
	for _, tc := range []struct {
		lat, lon float64
		want     int
	}{
		{21.0, 105.8, 0}, {-33.9, 151.2, 1}, {40.7, -74.0, 0}, {10.8, 106.7, 1},
	} {
		if got := Fold(tc.lat, tc.lon); got != tc.want {
			t.Errorf("Fold(%v, %v) = %d, want %d", tc.lat, tc.lon, got, tc.want)
		}
	}
	// Two centers in the same cell share a fold.
	if Fold(16, 106) != Fold(29, 119) {
		t.Error("one cell, two folds")
	}
}
