package backtest

import (
	"encoding/gob"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
)

// blockSec is the length of a resampling block: forecasts within six hours
// of each other in one region share weather, so they are resampled together.
const blockSec = 6 * 3600

// nBins is the resolution of the probability histograms.
const nBins = 20

// leadAcc accumulates one variant at one lead.
type leadAcc struct {
	H, M, F, C int64   // hits, misses, false alarms, correct negatives
	AbsErr     float64 // |pred − obs| dBZ where either side rains
	NErr       int64
	Brier      float64 // Σ (p − o)²
	NBrier     int64
	// AccumErr is |pred − obs| of the rain accumulated up to this lead (mm,
	// from the dBZ at every lead so far) where either side has rain.
	AccumErr float64
	NAccum   int64
}

func (a *leadAcc) add(o leadAcc) {
	a.H += o.H
	a.M += o.M
	a.F += o.F
	a.C += o.C
	a.AbsErr += o.AbsErr
	a.NErr += o.NErr
	a.Brier += o.Brier
	a.NBrier += o.NBrier
	a.AccumErr += o.AccumErr
	a.NAccum += o.NAccum
}

// block holds one region's scores over six hours: [variant][lead].
type block struct {
	Region  string
	Climate string
	Sat     bool // the region is seen by Himawari
	Start   int64
	Issues  int
	Acc     [][]leadAcc
	Cls     [][][]classAcc // [variant][class][lead]
	FSS     [][][][]fssAcc // [variant][threshold][window][lead]; nil until scored
	Nanos   []int64        // time spent per variant
}

// hist counts outcomes per forecast-probability bin.
type hist struct {
	Pos, Neg [nBins]int64
	SumP     [nBins]float64
}

func (h *hist) add(o hist) {
	for b := range nBins {
		h.Pos[b] += o.Pos[b]
		h.Neg[b] += o.Neg[b]
		h.SumP[b] += o.SumP[b]
	}
}

func binOf(p float32) int { return min(int(p*nBins), nBins-1) }

// corrAcc holds the sums for error correlations between variants.
type corrAcc struct {
	N    int64
	S    []float64   // Σ e_v
	S2   []float64   // Σ e_v²
	Prod [][]float64 // Σ e_v e_w
}

// regionState is what a region has contributed so far.
type regionState struct {
	Climate         string
	Sat             bool  // seen by Himawari
	Last            int64 // newest issue time scored
	Frames, Skipped int
	Points          int
	Rainy           map[int64]bool // central tile rains, per frame
}

// State is everything scored so far. It is saved between runs so a
// background run only adds new forecast times.
type State struct {
	Version string
	Blocks  map[string]*block
	// Hist[half][variant][lead]: the halves alternate by block, so
	// calibration learned on one is scored on the other.
	Hist    [2][][]hist
	Corr    corrAcc
	Regions map[string]*regionState
}

// NewState returns an empty state for cfg.
func NewState(cfg Config) *State {
	n := len(cfg.Variants) + 1
	st := &State{Version: cfg.version(), Blocks: map[string]*block{}, Regions: map[string]*regionState{}}
	for h := range st.Hist {
		st.Hist[h] = make([][]hist, n)
		for v := range n {
			st.Hist[h][v] = make([]hist, len(cfg.Leads))
		}
	}
	st.Corr = corrAcc{S: make([]float64, n), S2: make([]float64, n), Prod: make([][]float64, n)}
	for v := range n {
		st.Corr.Prod[v] = make([]float64, n)
	}
	return st
}

// version identifies the scoring setup; a saved state scored differently is
// discarded.
func (cfg Config) version() string {
	h := fnv.New64a()
	fmt.Fprint(h, "v5", cfg.Leads, cfg.Threshold, cfg.Radius, cfg.Strong, cfg.TrendTau, cfg.Step, cfg.StepSec, cfg.Classes,
		cfg.FSSThresholds, cfg.FSSWindows, cfg.Since)
	for _, v := range cfg.Variants {
		fmt.Fprint(h, v.Name, v.Method, v.Pairs, v.Trend, v.Members, v.MLSet)
	}
	return fmt.Sprintf("%x", h.Sum64())
}

func (st *State) lastIssue(region string) int64 {
	if r := st.Regions[region]; r != nil {
		return r.Last
	}
	return 0
}

func (st *State) issueCount() int {
	n := 0
	for _, b := range st.Blocks {
		n += b.Issues
	}
	return n
}

func blockKey(region string, t int64) string { return fmt.Sprintf("%s|%d", region, t/blockSec) }

func half(t int64) int { return int(t/blockSec) % 2 }

// block returns the block holding issue time t, creating it.
func (st *State) block(region, climate string, t int64, nVar, nCls, nLead int) *block {
	k := blockKey(region, t)
	b := st.Blocks[k]
	if b == nil {
		b = &block{Region: region, Climate: climate, Start: t / blockSec * blockSec,
			Acc: make([][]leadAcc, nVar), Cls: make([][][]classAcc, nVar), Nanos: make([]int64, nVar)}
		for v := range b.Acc {
			b.Acc[v] = make([]leadAcc, nLead)
			b.Cls[v] = make([][]classAcc, nCls)
			for k := range b.Cls[v] {
				b.Cls[v][k] = make([]classAcc, nLead)
			}
		}
		st.Blocks[k] = b
	}
	return b
}

// merge adds part, which scored different forecast times, into st.
func (st *State) merge(part *State) {
	for k, pb := range part.Blocks {
		b := st.Blocks[k]
		if b == nil {
			st.Blocks[k] = pb
			continue
		}
		b.Issues += pb.Issues
		for v := range b.Acc {
			for l := range b.Acc[v] {
				b.Acc[v][l].add(pb.Acc[v][l])
			}
			for k := range b.Cls[v] {
				for l := range b.Cls[v][k] {
					b.Cls[v][k][l].add(pb.Cls[v][k][l])
				}
			}
			b.Nanos[v] += pb.Nanos[v]
		}
		switch {
		case pb.FSS == nil:
		case b.FSS == nil:
			b.FSS = pb.FSS
		default:
			for v := range b.FSS {
				for t := range b.FSS[v] {
					for w := range b.FSS[v][t] {
						for l := range b.FSS[v][t][w] {
							b.FSS[v][t][w][l].add(pb.FSS[v][t][w][l])
						}
					}
				}
			}
		}
	}
	for h := range st.Hist {
		for v := range st.Hist[h] {
			for l := range st.Hist[h][v] {
				st.Hist[h][v][l].add(part.Hist[h][v][l])
			}
		}
	}
	c, pc := &st.Corr, &part.Corr
	c.N += pc.N
	for v := range c.S {
		c.S[v] += pc.S[v]
		c.S2[v] += pc.S2[v]
		for w := range c.Prod[v] {
			c.Prod[v][w] += pc.Prod[v][w]
		}
	}
	for name, pr := range part.Regions {
		r := st.Regions[name]
		if r == nil {
			st.Regions[name] = pr
			continue
		}
		r.Climate, r.Sat = pr.Climate, pr.Sat
		r.Last = max(r.Last, pr.Last)
		r.Frames += pr.Frames
		r.Skipped += pr.Skipped
		r.Points = max(r.Points, pr.Points)
		for t, v := range pr.Rainy {
			r.Rainy[t] = v
		}
	}
}

// Prune drops blocks and rain records older than cutoff (unix s), matching
// the tile cache's retention.
func (st *State) Prune(cutoff int64) {
	for k, b := range st.Blocks {
		if b.Start+blockSec < cutoff {
			delete(st.Blocks, k)
		}
	}
	for _, r := range st.Regions {
		for t := range r.Rainy {
			if t < cutoff {
				delete(r.Rainy, t)
			}
		}
	}
}

// LoadState reads a saved state, or returns nil when there is none.
func LoadState(path string) *State {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var st State
	if gob.NewDecoder(f).Decode(&st) != nil {
		return nil
	}
	return &st
}

// Save writes the state atomically.
func (st *State) Save(path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".btstate-*")
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(tmp).Encode(st); err != nil {
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

// sortedBlocks returns the blocks in a stable order.
func (st *State) sortedBlocks() []*block {
	keys := make([]string, 0, len(st.Blocks))
	for k := range st.Blocks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*block, len(keys))
	for i, k := range keys {
		out[i] = st.Blocks[k]
	}
	return out
}
