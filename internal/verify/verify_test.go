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
