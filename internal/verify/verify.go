// Package verify scores rain/no-rain forecasts against what was observed.
package verify

// Scores is a 2×2 contingency table and the usual skill scores derived from
// it. A score is nil when its denominator is zero.
type Scores struct {
	N           int      `json:"n"`
	Hits        int      `json:"hits"`
	Misses      int      `json:"misses"`
	FalseAlarms int      `json:"false_alarms"`
	CorrectNeg  int      `json:"correct_negatives"`
	Accuracy    *float64 `json:"accuracy"`
	POD         *float64 `json:"pod"` // probability of detection
	FAR         *float64 `json:"far"` // false alarm ratio
	CSI         *float64 `json:"csi"` // critical success index
}

// Add records one forecast/observation pair.
func (s *Scores) Add(pred, obs bool) {
	s.N++
	switch {
	case pred && obs:
		s.Hits++
	case !pred && obs:
		s.Misses++
	case pred && !obs:
		s.FalseAlarms++
	default:
		s.CorrectNeg++
	}
}

// AddN records n identical forecast/observation pairs.
func (s *Scores) AddN(pred, obs bool, n int) {
	s.N += n
	switch {
	case pred && obs:
		s.Hits += n
	case !pred && obs:
		s.Misses += n
	case pred && !obs:
		s.FalseAlarms += n
	default:
		s.CorrectNeg += n
	}
}

// Merge adds o's counts; call Compute afterwards.
func (s *Scores) Merge(o Scores) {
	s.N += o.N
	s.Hits += o.Hits
	s.Misses += o.Misses
	s.FalseAlarms += o.FalseAlarms
	s.CorrectNeg += o.CorrectNeg
}

// Compute fills the derived scores from the counts.
func (s *Scores) Compute() {
	s.Accuracy = ratio(s.Hits+s.CorrectNeg, s.N)
	s.POD = ratio(s.Hits, s.Hits+s.Misses)
	s.FAR = ratio(s.FalseAlarms, s.Hits+s.FalseAlarms)
	s.CSI = ratio(s.Hits, s.Hits+s.Misses+s.FalseAlarms)
}

func ratio(a, b int) *float64 {
	if b == 0 {
		return nil
	}
	v := float64(a) / float64(b)
	return &v
}
