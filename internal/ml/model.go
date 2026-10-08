package ml

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Feature sets: which features a model was trained on.
const (
	// SetRadar is every feature from the radar and the motion (no NWP, no
	// satellite): the ML the backtest reports. On held-out data NWP and the
	// satellite added under a CSI point (October 2026), so they are not
	// collected by default; the sets with them stay for re-checking.
	SetRadar = "radar"
	SetAll   = "all"
	SetNoNWP = "no_nwp"
	SetNoSat = "no_sat"
	// SetEns sees only the members' forecasts (lead, m_*): the ensemble
	// calibrated the way the ML is, so comparing it with SetRadar shows what
	// the other features add beyond moving the threshold.
	SetEns = "ens"
)

// Sets are the feature sets the backtest compares.
var Sets = []string{SetRadar, SetEns, SetAll, SetNoNWP, SetNoSat}

// Feature name prefixes of the optional sources.
const (
	PrefixNWP = "nwp_"
	PrefixSat = "sat_"
)

// Tree is one regression tree in flat arrays, as scripts/ml/train.py
// exports a LightGBM dump: node i splits on Feature[i] (an index into the
// bundle's Features) at Threshold[i], or is a leaf worth Value[i] when
// Feature[i] < 0.
type Tree struct {
	Feature     []int     `json:"feature"`
	Threshold   []float64 `json:"threshold"`
	Left        []int     `json:"left"`
	Right       []int     `json:"right"`
	DefaultLeft []bool    `json:"default_left"`
	// Missing is LightGBM's missing_type per node: 0 None (NaN counts as
	// 0), 1 Zero (0 and NaN go the default way), 2 NaN (NaN goes the
	// default way).
	Missing []uint8   `json:"missing"`
	Value   []float64 `json:"value"`
}

func (t *Tree) eval(x []float64) float64 {
	i := 0
	for t.Feature[i] >= 0 {
		v := x[t.Feature[i]]
		var left bool
		switch {
		case t.Missing[i] == 2 && math.IsNaN(v):
			left = t.DefaultLeft[i]
		case t.Missing[i] == 1 && (math.IsNaN(v) || math.Abs(v) <= 1e-35):
			left = t.DefaultLeft[i]
		default:
			if math.IsNaN(v) {
				v = 0
			}
			left = v <= t.Threshold[i]
		}
		if left {
			i = t.Left[i]
		} else {
			i = t.Right[i]
		}
	}
	return t.Value[i]
}

// Class is the model of one strength: will the echo reach DBZ?
type Class struct {
	DBZ   float32 `json:"dbz"`
	Trees []Tree  `json:"trees"`
	// CalibX, CalibY map the raw probability onto the observed frequency
	// (isotonic, interpolated linearly); empty leaves it as is.
	CalibX []float64 `json:"calib_x"`
	CalibY []float64 `json:"calib_y"`
	// PStar is, per lead (minutes, as a string key in JSON), the
	// probability above which the strength is forecast: the one that gave
	// the best CSI where it was learned.
	PStar map[int]float64 `json:"pstar"`
}

func (c *Class) prob(x []float64) float64 {
	var raw float64
	for i := range c.Trees {
		raw += c.Trees[i].eval(x)
	}
	p := 1 / (1 + math.Exp(-raw))
	return interp(c.CalibX, c.CalibY, p)
}

// pstar is PStar at lead, or at the nearest lead learned.
func (c *Class) pstar(lead int) float64 {
	if p, ok := c.PStar[lead]; ok {
		return p
	}
	best, bd := 0.5, math.MaxInt
	for l, p := range c.PStar {
		if d := abs(l - lead); d < bd || (d == bd && l < lead) {
			best, bd = p, d
		}
	}
	return best
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func interp(xs, ys []float64, x float64) float64 {
	if len(xs) == 0 {
		return x
	}
	i := sort.SearchFloat64s(xs, x)
	switch {
	case i == 0:
		return ys[0]
	case i >= len(xs):
		return ys[len(ys)-1]
	}
	if xs[i] == xs[i-1] {
		return ys[i]
	}
	w := (x - xs[i-1]) / (xs[i] - xs[i-1])
	return ys[i-1]*(1-w) + ys[i]*w
}

// Bundle is one feature set's models for every strength, ascending.
type Bundle struct {
	Set      string   `json:"set"`
	Fold     int      `json:"fold"` // -1: trained on everything
	Features []string `json:"features"`
	Classes  []Class  `json:"classes"`
	// Trained notes what the bundle was learned from, for the report.
	Trained string `json:"trained,omitempty"`

	index []int // Features → Names
}

// Load reads a bundle exported by scripts/ml/train.py.
func Load(path string) (*Bundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse reads a bundle from JSON.
func Parse(data []byte) (*Bundle, error) {
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	b.index = make([]int, len(b.Features))
	for i, n := range b.Features {
		if b.index[i] = Index(n); b.index[i] < 0 {
			return nil, fmt.Errorf("ml: unknown feature %q", n)
		}
	}
	if len(b.Classes) == 0 {
		return nil, fmt.Errorf("ml: bundle has no classes")
	}
	for k := range b.Classes {
		for t := range b.Classes[k].Trees {
			tr := &b.Classes[k].Trees[t]
			n := len(tr.Feature)
			if n == 0 || len(tr.Threshold) != n || len(tr.Left) != n || len(tr.Right) != n ||
				len(tr.DefaultLeft) != n || len(tr.Missing) != n || len(tr.Value) != n {
				return nil, fmt.Errorf("ml: class %d tree %d is malformed", k, t)
			}
			for i, f := range tr.Feature {
				if f >= len(b.Features) || (f >= 0 && (tr.Left[i] <= i || tr.Right[i] <= i || tr.Left[i] >= n || tr.Right[i] >= n)) {
					return nil, fmt.Errorf("ml: class %d tree %d node %d is malformed", k, t, i)
				}
			}
		}
	}
	return &b, nil
}

// Uses reports whether b reads any feature starting with prefix
// (PrefixNWP, PrefixSat).
func (b *Bundle) Uses(prefix string) bool {
	for _, f := range b.Features {
		if strings.HasPrefix(f, prefix) {
			return true
		}
	}
	return false
}

// LoadDir reads the bundles in dir named <set>_fold<k>.json (k = 0, 1) and
// <set>.json; sets with a fold missing are left out.
func LoadDir(dir string) (folds map[string][2]*Bundle, full map[string]*Bundle, err error) {
	folds, full = map[string][2]*Bundle{}, map[string]*Bundle{}
	for _, set := range Sets {
		var pair [2]*Bundle
		ok := true
		for k := range 2 {
			b, err := Load(filepath.Join(dir, fmt.Sprintf("%s_fold%d.json", set, k)))
			if os.IsNotExist(err) {
				ok = false
				break
			}
			if err != nil {
				return nil, nil, err
			}
			pair[k] = b
		}
		if ok {
			folds[set] = pair
		}
		b, err := Load(filepath.Join(dir, set+".json"))
		if err == nil {
			full[set] = b
		} else if !os.IsNotExist(err) {
			return nil, nil, err
		}
	}
	return folds, full, nil
}

// Predict returns, for a feature row (Names order), the probability of
// reaching each class's strength, never rising with strength.
func (b *Bundle) Predict(row []float32) []float64 {
	x := make([]float64, len(b.index))
	for i, j := range b.index {
		x[i] = float64(row[j])
	}
	ps := make([]float64, len(b.Classes))
	for k := range b.Classes {
		ps[k] = b.Classes[k].prob(x)
		if k > 0 && ps[k] > ps[k-1] {
			ps[k] = ps[k-1]
		}
	}
	return ps
}

// Apply turns the member mean e (dBZ) into the forecast: the strongest
// class whose probability clears its PStar at lead sets the band e must
// lie in, [its DBZ, the next class's DBZ); with none, e is kept below the
// first. Inside the band e is kept, so a strong core is never pulled down
// to the mean.
func (b *Bundle) Apply(e float32, lead int, ps []float64) float32 {
	top := -1
	for k := range b.Classes {
		if ps[k] >= b.Classes[k].pstar(lead) {
			top = k
		}
	}
	const below = 0.01
	if top < 0 {
		return min(e, b.Classes[0].DBZ-below)
	}
	lo := b.Classes[top].DBZ
	if e < lo {
		return lo
	}
	if top+1 < len(b.Classes) {
		if hi := b.Classes[top+1].DBZ; e >= hi {
			return hi - below
		}
	}
	return e
}
