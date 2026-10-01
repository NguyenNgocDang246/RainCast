package radar

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"image/color"
	"strconv"
	"strings"
)

// MinDBZ is the value used for pixels with no echo.
const MinDBZ float32 = -32

//go:embed palette.csv
var paletteCSV string

// paletteColumn is the scheme RainViewer serves for every color id.
const paletteColumn = "Universal Blue"

// Palette maps RainViewer tile colors to reflectivity (dBZ) and back.
type Palette struct {
	toDBZ   map[color.NRGBA]float32
	entries []paletteEntry // ascending dBZ, opaque-ish colors only
}

type paletteEntry struct {
	dbz float32
	c   color.NRGBA
}

// DefaultPalette is parsed from the embedded RainViewer color table.
var DefaultPalette = mustParsePalette(paletteCSV)

func mustParsePalette(src string) *Palette {
	p, err := ParsePalette(src)
	if err != nil {
		panic(err)
	}
	return p
}

// ParsePalette reads the rain section of RainViewer's color table CSV.
// The file repeats the dBZ range a second time for snow; that section is skipped
// because tiles are requested with snow disabled.
func ParsePalette(src string) (*Palette, error) {
	rows, err := csv.NewReader(strings.NewReader(src)).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("palette: %w", err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("palette: empty table")
	}
	col := -1
	for i, h := range rows[0] {
		if h == paletteColumn {
			col = i
		}
	}
	if col < 0 {
		return nil, fmt.Errorf("palette: column %q not found", paletteColumn)
	}

	p := &Palette{toDBZ: make(map[color.NRGBA]float32)}
	prev := -1 << 31
	for _, r := range rows[1:] {
		dbz, err := strconv.Atoi(r[0])
		if err != nil {
			return nil, fmt.Errorf("palette: bad dBZ %q", r[0])
		}
		if dbz <= prev {
			break // start of the snow section
		}
		prev = dbz
		c, err := parseHex(r[col])
		if err != nil {
			return nil, err
		}
		if c.A == 0 {
			continue
		}
		// Several high values share a color; keep the lowest dBZ.
		if _, ok := p.toDBZ[c]; !ok {
			p.toDBZ[c] = float32(dbz)
		}
		p.entries = append(p.entries, paletteEntry{float32(dbz), c})
	}
	return p, nil
}

func parseHex(s string) (color.NRGBA, error) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 8 {
		return color.NRGBA{}, fmt.Errorf("palette: bad color %q", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.NRGBA{}, fmt.Errorf("palette: bad color %q", s)
	}
	return color.NRGBA{R: uint8(v >> 24), G: uint8(v >> 16), B: uint8(v >> 8), A: uint8(v)}, nil
}

// Lookup returns the dBZ for c. exact is false when c is not in the table and
// the nearest entry was used instead.
func (p *Palette) Lookup(c color.NRGBA) (dbz float32, exact bool) {
	if c.A == 0 {
		return MinDBZ, true
	}
	if v, ok := p.toDBZ[c]; ok {
		return v, true
	}
	best, bestD := MinDBZ, int64(1<<62)
	for _, e := range p.entries {
		dr, dg, db, da := int64(c.R)-int64(e.c.R), int64(c.G)-int64(e.c.G), int64(c.B)-int64(e.c.B), int64(c.A)-int64(e.c.A)
		if d := dr*dr + dg*dg + db*db + da*da; d < bestD {
			best, bestD = e.dbz, d
		}
	}
	return best, false
}

// Color returns the display color for dbz.
func (p *Palette) Color(dbz float32) color.NRGBA {
	var out color.NRGBA
	for _, e := range p.entries {
		if e.dbz > dbz {
			break
		}
		out = e.c
	}
	return out
}
