package backtest

import (
	"encoding/gob"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"slices"
)

// Weighting schemes for MethodWeighted ensembles.
const (
	WeightEqual   = "equal"   // every member alike
	WeightSkill   = "skill"   // ∝ CSI gain over persistence, all leads together
	WeightLead    = "lead"    // ∝ CSI gain over persistence, per lead
	WeightRegress = "regress" // least squares on dBZ, per lead, non-negative, summing to 1
)

// MethodWeighted combines Members with weights learned from data.
const MethodWeighted = "weighted"

// To keep the comparison fair, weights are never learned from the data they
// are scored on: regions fall into two folds, and each fold is scored with
// weights learned on the other one, from the statistics of the previous
// run.

// fold puts a region in one of the two folds.
func fold(region string) int {
	h := fnv.New32a()
	h.Write([]byte(region))
	return int(h.Sum32() % 2)
}

// memberStats are the sufficient statistics for learning weights over the
// members of the weighted ensembles, for one region and lead.
type memberStats struct {
	H, M, F []int64     // per member, then persistence last
	XtX     [][]float64 // Σ x xᵀ over member dBZ (clipped at 0)
	Xty     []float64   // Σ x·obs
	N       int64
}

func newMemberStats(k int) *memberStats {
	s := &memberStats{H: make([]int64, k+1), M: make([]int64, k+1), F: make([]int64, k+1),
		XtX: make([][]float64, k), Xty: make([]float64, k)}
	for i := range s.XtX {
		s.XtX[i] = make([]float64, k)
	}
	return s
}

func (s *memberStats) add(o *memberStats) {
	for i := range s.H {
		s.H[i] += o.H[i]
		s.M[i] += o.M[i]
		s.F[i] += o.F[i]
	}
	for i := range s.XtX {
		for j := range s.XtX[i] {
			s.XtX[i][j] += o.XtX[i][j]
		}
		s.Xty[i] += o.Xty[i]
	}
	s.N += o.N
}

// WeightStats are the per-region statistics weights are learned from. They
// live in their own file, kept across changes to the scored variants.
type WeightStats struct {
	Members []string
	Leads   []int
	Regions map[string][]*memberStats // per lead
}

func newWeightStats(members []string, leads []int) *WeightStats {
	return &WeightStats{Members: members, Leads: leads, Regions: map[string][]*memberStats{}}
}

func (ws *WeightStats) region(name string) []*memberStats {
	r := ws.Regions[name]
	if r == nil {
		r = make([]*memberStats, len(ws.Leads))
		for l := range r {
			r[l] = newMemberStats(len(ws.Members))
		}
		ws.Regions[name] = r
	}
	return r
}

func (ws *WeightStats) merge(o *WeightStats) {
	for name, ls := range o.Regions {
		r := ws.region(name)
		for l := range ls {
			r[l].add(ls[l])
		}
	}
}

// LoadWeightStats reads saved statistics; nil when missing or for other
// members or leads.
func LoadWeightStats(path string, members []string, leads []int) *WeightStats {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var ws WeightStats
	if gob.NewDecoder(f).Decode(&ws) != nil || !slices.Equal(ws.Members, members) || !slices.Equal(ws.Leads, leads) {
		return nil
	}
	return &ws
}

// Save writes the statistics atomically.
func (ws *WeightStats) Save(path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".btweights-*")
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(tmp).Encode(ws); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Weights are learned weights: [fold][scheme][lead][member]. A fold's
// weights come from the other fold's regions.
type Weights struct {
	byFold [2]map[string][][]float64
	// Learned reports whether the weights came from data; without any, every
	// scheme falls back to equal weights.
	Learned bool
}

// LearnWeights derives every scheme's weights for both folds.
func LearnWeights(ws *WeightStats) *Weights {
	w := &Weights{}
	if ws == nil {
		return w
	}
	k, nLead := len(ws.Members), len(ws.Leads)
	for f := range 2 {
		agg := make([]*memberStats, nLead)
		for l := range agg {
			agg[l] = newMemberStats(k)
		}
		for name, ls := range ws.Regions {
			if fold(name) == f {
				continue // learn from the other fold only
			}
			for l := range ls {
				agg[l].add(ls[l])
				w.Learned = w.Learned || ls[l].N > 0
			}
		}
		all := newMemberStats(k)
		for _, a := range agg {
			all.add(a)
		}
		m := map[string][][]float64{}
		for _, scheme := range []string{WeightEqual, WeightSkill, WeightLead, WeightRegress} {
			m[scheme] = make([][]float64, nLead)
			for l := range nLead {
				switch scheme {
				case WeightSkill:
					m[scheme][l] = skillWeights(all)
				case WeightLead:
					m[scheme][l] = skillWeights(agg[l])
				case WeightRegress:
					m[scheme][l] = regressWeights(agg[l])
				default:
					m[scheme][l] = equal(k)
				}
			}
		}
		w.byFold[f] = m
	}
	return w
}

// For returns the weights for a region's fold, scheme and lead index.
func (w *Weights) For(region, scheme string, lead, k int) []float64 {
	if w == nil || w.byFold[fold(region)] == nil {
		return equal(k)
	}
	return w.byFold[fold(region)][scheme][lead]
}

func equal(k int) []float64 {
	out := make([]float64, k)
	for i := range out {
		out[i] = 1 / float64(k)
	}
	return out
}

func csi(h, m, f int64) float64 {
	if d := h + m + f; d > 0 {
		return float64(h) / float64(d)
	}
	return 0
}

// skillWeights are proportional to each member's CSI gain over persistence.
func skillWeights(s *memberStats) []float64 {
	k := len(s.XtX)
	base := csi(s.H[k], s.M[k], s.F[k])
	out := make([]float64, k)
	var sum float64
	for i := range k {
		out[i] = math.Max(csi(s.H[i], s.M[i], s.F[i])-base, 0)
		sum += out[i]
	}
	if sum == 0 {
		return equal(k)
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}

// regressWeights minimize Σ (obs − Σ wᵢ xᵢ)² with wᵢ ≥ 0 and Σ wᵢ = 1, by
// trying every subset of active members (there are only a few).
func regressWeights(s *memberStats) []float64 {
	k := len(s.XtX)
	if s.N == 0 {
		return equal(k)
	}
	best, bestLoss := equal(k), math.Inf(1)
	for mask := 1; mask < 1<<k; mask++ {
		var idx []int
		for i := range k {
			if mask&(1<<i) != 0 {
				idx = append(idx, i)
			}
		}
		w, ok := constrainedLS(s, idx)
		if !ok {
			continue
		}
		// Loss up to a constant: wᵀ XtX w − 2 wᵀ Xty.
		var loss float64
		for i := range k {
			for j := range k {
				loss += w[i] * s.XtX[i][j] * w[j]
			}
			loss -= 2 * w[i] * s.Xty[i]
		}
		if loss < bestLoss {
			best, bestLoss = w, loss
		}
	}
	return best
}

// constrainedLS solves least squares over members idx with weights summing
// to 1 (Lagrange system); ok is false when a weight comes out negative or
// the system is singular.
func constrainedLS(s *memberStats, idx []int) ([]float64, bool) {
	n := len(idx)
	// [A 1; 1ᵀ 0] [w; λ] = [b; 1]
	a := make([][]float64, n+1)
	for r := range a {
		a[r] = make([]float64, n+2)
	}
	for r, i := range idx {
		for c, j := range idx {
			a[r][c] = s.XtX[i][j]
		}
		a[r][n] = 1
		a[r][n+1] = s.Xty[i]
		a[n][r] = 1
	}
	a[n][n+1] = 1
	// Gaussian elimination with partial pivoting.
	for c := range n + 1 {
		p := c
		for r := c + 1; r <= n; r++ {
			if math.Abs(a[r][c]) > math.Abs(a[p][c]) {
				p = r
			}
		}
		if math.Abs(a[p][c]) < 1e-12 {
			return nil, false
		}
		a[c], a[p] = a[p], a[c]
		for r := range n + 1 {
			if r == c {
				continue
			}
			f := a[r][c] / a[c][c]
			for cc := c; cc <= n+1; cc++ {
				a[r][cc] -= f * a[c][cc]
			}
		}
	}
	out := make([]float64, len(s.XtX))
	for r, i := range idx {
		v := a[r][n+1] / a[r][r]
		if v < -1e-9 {
			return nil, false
		}
		out[i] = math.Max(v, 0)
	}
	return out, true
}
