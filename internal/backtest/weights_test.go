package backtest

import (
	"math"
	"math/rand/v2"
	"path/filepath"
	"testing"
)

// The observation is 0.7·a + 0.3·b plus noise; c is noise only.
func TestRegressWeightsRecoverMix(t *testing.T) {
	s := newMemberStats(3)
	r := rand.New(rand.NewPCG(1, 1))
	for range 20000 {
		a, b, c := r.Float64()*50, r.Float64()*50, r.Float64()*50
		y := 0.7*a + 0.3*b + r.NormFloat64()
		x := []float64{a, b, c}
		for i := range x {
			for j := range x {
				s.XtX[i][j] += x[i] * x[j]
			}
			s.Xty[i] += x[i] * y
		}
		s.N++
	}
	w := regressWeights(s)
	if math.Abs(w[0]-0.7) > 0.02 || math.Abs(w[1]-0.3) > 0.02 || w[2] > 0.02 {
		t.Fatalf("weights %v, want ≈ [0.7 0.3 0]", w)
	}
	if sum := w[0] + w[1] + w[2]; math.Abs(sum-1) > 1e-9 {
		t.Fatalf("weights sum to %v", sum)
	}
}

func TestSkillWeights(t *testing.T) {
	s := newMemberStats(2)
	// Persistence CSI 0.5; member 0 gains 0.3, member 1 gains 0.1.
	s.H[0], s.M[0], s.F[0] = 8, 1, 1
	s.H[1], s.M[1], s.F[1] = 6, 2, 2
	s.H[2], s.M[2], s.F[2] = 5, 3, 2
	w := skillWeights(s)
	if math.Abs(w[0]-0.75) > 1e-9 || math.Abs(w[1]-0.25) > 1e-9 {
		t.Fatalf("weights %v, want [0.75 0.25]", w)
	}
}

// Each fold must learn only from the other: a region's own statistics
// never shape the weights it is scored with.
func TestWeightsLearnFromOtherFold(t *testing.T) {
	ws := newWeightStats([]string{"a", "b"}, []int{10})
	var r0, r1 string
	for i := 0; r0 == "" || r1 == ""; i++ {
		name := string(rune('A' + i))
		if fold(name) == 0 && r0 == "" {
			r0 = name
		}
		if fold(name) == 1 && r1 == "" {
			r1 = name
		}
	}
	// In r0's fold member a is perfect; in r1's fold member b is.
	set := func(region string, best int) {
		s := ws.region(region)[0]
		s.H[best], s.M[best], s.F[best] = 10, 0, 0
		s.H[1-best], s.M[1-best], s.F[1-best] = 5, 5, 5
		s.H[2], s.M[2], s.F[2] = 4, 6, 6
		s.N = 1
	}
	set(r0, 0)
	set(r1, 1)
	w := LearnWeights(ws)
	if !w.Learned {
		t.Fatal("not learned")
	}
	if got := w.For(r0, WeightLead, 0, 2); got[1] <= got[0] {
		t.Errorf("fold of %s weights %v: should favor b, learned on the other fold", r0, got)
	}
	if got := w.For(r1, WeightLead, 0, 2); got[0] <= got[1] {
		t.Errorf("fold of %s weights %v: should favor a", r1, got)
	}
}

func TestWeightStatsSaveLoad(t *testing.T) {
	ws := newWeightStats([]string{"a", "b"}, []int{10, 20})
	ws.region("r")[1].N = 7
	path := filepath.Join(t.TempDir(), "w.gob")
	if err := ws.Save(path); err != nil {
		t.Fatal(err)
	}
	got := LoadWeightStats(path, []string{"a", "b"}, []int{10, 20})
	if got == nil || got.Regions["r"][1].N != 7 {
		t.Fatalf("reloaded %+v", got)
	}
	if LoadWeightStats(path, []string{"a", "c"}, []int{10, 20}) != nil {
		t.Error("statistics for other members must be ignored")
	}
}
