package ml

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"math"
	"os"
	"sync"
)

// Leading columns of a dumped row, before the features (Names).
var Leading = []string{"region", "fold", "t_min", "lead", "weight", "obs"}

// DryKeep is the share of quiet rows dumped (no rain near the point now,
// in any member or in the observation); each kept one weighs 1/DryKeep.
const DryKeep = 0.05

// QuietDBZ is the echo below which a row counts as quiet.
const QuietDBZ = 10

// FoldCellDeg is the side, in degrees of latitude and longitude, of the
// cells Fold assigns whole.
const FoldCellDeg = 15

// Fold puts a region in one of two folds by the FoldCellDeg cell its
// center falls in, so a model is always scored on regions it never saw and
// neighbours, which share storms and weather at the same times, land in
// the same fold. scripts/ml/train.py repeats this rule (fold_of).
func Fold(lat, lon float64) int {
	cy := int(math.Floor(lat / FoldCellDeg))
	cx := int(math.Floor(lon / FoldCellDeg))
	return int(crc32.ChecksumIEEE([]byte(fmt.Sprintf("%d,%d", cy, cx))) % 2)
}

// RegionInfo describes a dumped region.
type RegionInfo struct {
	ID      int     `json:"id"`
	Name    string  `json:"name"`
	Climate string  `json:"climate"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Fold    int     `json:"fold"`
	Sat     bool    `json:"sat"` // Himawari sees it
}

// Writer dumps rows for training: float32 little-endian, len(Leading) +
// len(Names) columns, with a JSON sidecar (path + ".json") naming them.
// It is safe for concurrent use.
type Writer struct {
	path string
	f    *os.File
	w    *bufio.Writer

	mu      sync.Mutex
	regions map[string]*RegionInfo
	rows    int64
	err     error
}

// Create starts a dump at path.
func Create(path string) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &Writer{path: path, f: f, w: bufio.NewWriterSize(f, 1<<20), regions: map[string]*RegionInfo{}}, nil
}

// Region registers a region and returns its id.
func (w *Writer) Region(r RegionInfo) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if old, ok := w.regions[r.Name]; ok {
		return old.ID
	}
	r.ID = len(w.regions)
	w.regions[r.Name] = &r
	return r.ID
}

// Keep reports whether a row is dumped and its weight: every active row,
// and a fixed DryKeep share of the quiet ones, picked by hash so reruns
// agree.
func Keep(quiet bool, region int, t int64, lead, point int) (bool, float32) {
	if !quiet {
		return true, 1
	}
	h := fnv.New64a()
	var b [24]byte
	binary.LittleEndian.PutUint64(b[0:], uint64(region))
	binary.LittleEndian.PutUint64(b[8:], uint64(t))
	binary.LittleEndian.PutUint32(b[16:], uint32(lead))
	binary.LittleEndian.PutUint32(b[20:], uint32(point))
	h.Write(b[:])
	if float64(h.Sum64()%1_000_000)/1e6 < DryKeep {
		return true, 1 / DryKeep
	}
	return false, 0
}

// Rows appends rows; each is len(Leading)+len(Names) values.
func (w *Writer) Rows(rows [][]float32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return
	}
	buf := make([]byte, 4*(len(Leading)+len(Names)))
	for _, r := range rows {
		for i, v := range r {
			binary.LittleEndian.PutUint32(buf[4*i:], math.Float32bits(v))
		}
		if _, err := w.w.Write(buf); err != nil {
			w.err = err
			return
		}
		w.rows++
	}
}

// Close flushes the rows and writes the sidecar.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.w.Flush(); err != nil && w.err == nil {
		w.err = err
	}
	if err := w.f.Close(); err != nil && w.err == nil {
		w.err = err
	}
	if w.err != nil {
		return w.err
	}
	regions := make([]*RegionInfo, len(w.regions))
	for _, r := range w.regions {
		regions[r.ID] = r
	}
	meta := map[string]any{
		"columns": append(append([]string{}, Leading...), Names...),
		"leading": len(Leading),
		"rows":    w.rows,
		"dtype":   "float32",
		"regions": regions,
		// t_min is minutes since this epoch, to stay exact in float32.
		"t_epoch":  TEpoch,
		"dry_keep": DryKeep,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(w.path+".json", data, 0o644)
}

// TEpoch is subtracted from issue times before they are stored in minutes.
const TEpoch = 1_700_000_000
