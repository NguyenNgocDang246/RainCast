package radar

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"testing"

	"raincast/internal/geo"
)

func TestPaletteRoundTrip(t *testing.T) {
	p := DefaultPalette
	if len(p.entries) < 50 {
		t.Fatalf("only %d palette entries", len(p.entries))
	}
	for _, e := range p.entries {
		got, exact := p.Lookup(e.c)
		if !exact {
			t.Fatalf("color %v not exact", e.c)
		}
		// Shared colors resolve to the lowest dBZ that uses them.
		if p.Color(got) != e.c {
			t.Errorf("dBZ %v: color(lookup) = %v, want %v", e.dbz, p.Color(got), e.c)
		}
	}
	if v, _ := p.Lookup(color.NRGBA{}); v != MinDBZ {
		t.Errorf("transparent = %v, want %v", v, MinDBZ)
	}
}

func TestDecodeSampleTile(t *testing.T) {
	data, err := os.ReadFile("testdata/tile_7_101_60.png")
	if err != nil {
		t.Fatal(err)
	}
	g, err := Decode(data, DefaultPalette)
	if err != nil {
		t.Fatal(err)
	}
	if g.W != 256 || g.H != 256 {
		t.Fatalf("size %dx%d", g.W, g.H)
	}
	var rain int
	for _, v := range g.Data {
		if v >= 20 {
			rain++
		}
	}
	if rain == 0 {
		t.Fatal("sample tile has rain but none decoded")
	}
}

func TestDecodeRejectsPlaceholder(t *testing.T) {
	data, err := os.ReadFile("testdata/zoom_not_supported.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data, DefaultPalette); !errors.Is(err, ErrNotRadar) {
		t.Fatalf("err = %v, want ErrNotRadar", err)
	}
}

func TestBuildMosaic(t *testing.T) {
	c := DefaultPalette.Color(40)
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	img.SetNRGBA(10, 20, c)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	m, err := BuildMosaic(map[TileKey][]byte{{5, 7}: buf.Bytes()}, 7, 4, 6, 3, DefaultPalette)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := DefaultPalette.Lookup(c)
	if got := m.At(256+10, 256+20); got != want {
		t.Fatalf("center tile pixel = %v, want %v", got, want)
	}
	if got := m.At(10, 20); got != MinDBZ {
		t.Fatalf("missing tile pixel = %v, want MinDBZ", got)
	}
	x, y := m.Local(5*256+10, 7*256+20)
	if x != 266 || y != 276 {
		t.Fatalf("Local = (%v,%v)", x, y)
	}
}

func TestMedianInRadius(t *testing.T) {
	g := NewGrid(9, 9)
	// Rain on the west half of the disc only: 6 of 13 pixels.
	for y := 0; y < 9; y++ {
		for x := 0; x < 4; x++ {
			g.Set(x, y, 30)
		}
	}
	if v := g.MedianInRadius(4, 4, 2); v != MinDBZ {
		t.Fatalf("edge of a cell = %v, want no rain", v)
	}
	for y := 0; y < 9; y++ {
		g.Set(4, y, 30)
	}
	if v := g.MedianInRadius(4, 4, 2); v != 30 {
		t.Fatalf("mostly covered = %v, want 30", v)
	}
}

// Regression: at 18:40 on 2026-10-01 the radar showed no echo at this point
// (Thủ Đức), only a light band ~4 km west. Taking the max over a 3 px radius
// reported "light rain"; the median must not.
func TestNoRainNextToBand(t *testing.T) {
	data, err := os.ReadFile("testdata/tile_7_101_60_1840.png")
	if err != nil {
		t.Fatal(err)
	}
	g, err := Decode(data, DefaultPalette)
	if err != nil {
		t.Fatal(err)
	}
	gx, gy := geo.LatLonToIndex(10.890980609372656, 106.7926682027152, 7)
	x, y := gx-101*256, gy-60*256
	if v := g.MedianInRadius(x, y, 2); v >= 20 {
		t.Fatalf("median = %v dBZ, want < 20 (no rain)", v)
	}
}

func TestRainRate(t *testing.T) {
	cases := []struct {
		dbz  float32
		want float64
	}{{10, 0}, {20, 0.65}, {40, 11.5}, {55, 100}, {65, 100}}
	for _, c := range cases {
		if got := RainRate(c.dbz); math.Abs(got-c.want) > 0.02*c.want+1e-9 {
			t.Errorf("RainRate(%v) = %.3f mm/h, want ≈ %.3f", c.dbz, got, c.want)
		}
	}
}

func TestDBZInvertsRainRate(t *testing.T) {
	for _, mmh := range []float64{1, 4, 8, 15} {
		if got := RainRate(DBZ(mmh)); math.Abs(got-mmh) > 1e-3*mmh {
			t.Errorf("RainRate(DBZ(%v)) = %.4f", mmh, got)
		}
	}
	if d := DBZ(1); math.Abs(float64(d)-23.01) > 0.01 {
		t.Errorf("DBZ(1) = %.2f, want 23.01", d)
	}
}
