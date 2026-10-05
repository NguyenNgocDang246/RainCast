package backtest

// The fractions skill score (Roberts & Lean, 2008) judges a forecast on the
// share of rain in a window around each point instead of at the point, so
// rain forecast a few kilometres off still earns credit: closer to what a
// user means by "rain near me". Windows are counted in sample points of the
// lattice the backtest scores on (Config.Step pixels apart); the first,
// one point wide, is the plain point score.

// pxKm is about the ground size of a pixel at the radar zoom, only for
// labelling windows in kilometres.
const pxKm = 1.2

// fssAcc accumulates one variant, threshold, window and lead.
type fssAcc struct {
	Num float64 // Σ (Pf − Po)²
	Den float64 // Σ Pf² + Po²
}

func (a *fssAcc) add(o fssAcc) {
	a.Num += o.Num
	a.Den += o.Den
}

func (a fssAcc) score() *float64 {
	if a.Den == 0 {
		return nil
	}
	v := 1 - a.Num/a.Den
	return &v
}

// lattice places the sample points on their grid, for window counts.
type lattice struct {
	nx, ny int
	gx, gy []int // per point
}

func (l *lattice) put(gx, gy int) {
	l.gx = append(l.gx, gx)
	l.gy = append(l.gy, gy)
	l.nx, l.ny = max(l.nx, gx+1), max(l.ny, gy+1)
}

// table fills dst, (nx+1)·(ny+1) long, with the summed-area table of the
// points where on holds.
func (l *lattice) table(dst []int32, on func(p int) bool) {
	clear(dst)
	w := l.nx + 1
	for p := range l.gx {
		if on(p) {
			dst[(l.gy[p]+1)*w+l.gx[p]+1] = 1
		}
	}
	for y := 1; y <= l.ny; y++ {
		for x := 1; x <= l.nx; x++ {
			i := y*w + x
			dst[i] += dst[i-1] + dst[i-w] - dst[i-w-1]
		}
	}
}

// count is how many points of table s lie within h lattice steps of point p.
func (l *lattice) count(s []int32, p, h int) int32 {
	w := l.nx + 1
	x0, y0 := max(l.gx[p]-h, 0), max(l.gy[p]-h, 0)
	x1, y1 := min(l.gx[p]+h+1, l.nx), min(l.gy[p]+h+1, l.ny)
	return s[y1*w+x1] - s[y0*w+x1] - s[y1*w+x0] + s[y0*w+x0]
}

// fssAccs returns the block's accumulators, [variant][threshold][window][lead],
// creating them.
func (b *block) fssAccs(nVar, nThr, nWin, nLead int) [][][][]fssAcc {
	if b.FSS == nil {
		b.FSS = make([][][][]fssAcc, nVar)
		for v := range b.FSS {
			b.FSS[v] = make([][][]fssAcc, nThr)
			for t := range b.FSS[v] {
				b.FSS[v][t] = make([][]fssAcc, nWin)
				for w := range b.FSS[v][t] {
					b.FSS[v][t][w] = make([]fssAcc, nLead)
				}
			}
		}
	}
	return b.FSS
}

// addFSS scores every variant's rain fractions against the observed ones,
// windows only counting points with radar coverage (those on the lattice).
func addFSS(acc [][][][]fssAcc, fc []forecast, obs [][]float32, lat *lattice, cfg Config) {
	n := (lat.nx + 1) * (lat.ny + 1)
	cover, so, sf := make([]int32, n), make([]int32, n), make([]int32, n)
	lat.table(cover, func(int) bool { return true })
	for ti, thr := range cfg.FSSThresholds {
		for li := range cfg.Leads {
			lat.table(so, func(p int) bool { return obs[li][p] >= thr })
			for v := range fc {
				lat.table(sf, func(p int) bool { return fc[v].dbz[li][p] >= thr })
				for wi, win := range cfg.FSSWindows {
					h := win / 2
					var a fssAcc
					for p := range lat.gx {
						c := float64(lat.count(cover, p, h))
						pf := float64(lat.count(sf, p, h)) / c
						po := float64(lat.count(so, p, h)) / c
						a.Num += (pf - po) * (pf - po)
						a.Den += pf*pf + po*po
					}
					acc[v][ti][wi][li].add(a)
				}
			}
		}
	}
}

// FSSScore is a result's fractions skill score at one threshold and window.
type FSSScore struct {
	Threshold float32 `json:"threshold"` // dBZ
	Window    int     `json:"window"`    // sample points across
	WindowKm  float64 `json:"window_km"` // about
	// Leads follow Config.Leads; Overall pools them.
	Leads   []*float64 `json:"leads"`
	Overall *float64   `json:"overall"`
	// DeltaFSS is Overall minus the reference's.
	DeltaFSS *float64 `json:"delta_fss,omitempty"`
}

// fssResults adds every threshold and window's FSS to res (one per variant).
func fssResults(cfg Config, blocks []*block, res []Result) {
	nVar, nLead := len(res), len(cfg.Leads)
	ref := 0
	for i, r := range res {
		if r.Name == cfg.Reference {
			ref = i
		}
	}
	for ti, thr := range cfg.FSSThresholds {
		for wi, win := range cfg.FSSWindows {
			for v := range nVar {
				sums := make([]fssAcc, nLead)
				for _, b := range blocks {
					if len(b.FSS) != nVar {
						continue
					}
					for l, a := range b.FSS[v][ti][wi] {
						sums[l].add(a)
					}
				}
				s := FSSScore{Threshold: thr, Window: win, WindowKm: float64(win*cfg.Step) * pxKm}
				var all fssAcc
				for _, a := range sums {
					all.add(a)
					s.Leads = append(s.Leads, a.score())
				}
				s.Overall = all.score()
				res[v].FSS = append(res[v].FSS, s)
			}
			k := len(res[ref].FSS) - 1
			for v := range nVar {
				a, r := res[v].FSS[k].Overall, res[ref].FSS[k].Overall
				if v != ref && a != nil && r != nil {
					d := *a - *r
					res[v].FSS[k].DeltaFSS = &d
				}
			}
		}
	}
}
