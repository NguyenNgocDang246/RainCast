package radar

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

const tileSize = 256

// ErrNotRadar means the image does not look like a radar tile (e.g. the
// "Zoom Level Not Supported" placeholder RainViewer serves with HTTP 200).
var ErrNotRadar = errors.New("radar: image is not a radar tile")

// maxUnknownFrac is the share of non-transparent pixels allowed to miss the
// palette before a tile is rejected.
const maxUnknownFrac = 0.2

// Decode converts a RainViewer PNG tile into a dBZ grid.
func Decode(data []byte, p *Palette) (*Grid, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("radar: decode png: %w", err)
	}
	b := img.Bounds()
	g := &Grid{W: b.Dx(), H: b.Dy(), Data: make([]float32, b.Dx()*b.Dy())}

	type lookup struct {
		dbz   float32
		exact bool
	}
	cache := make(map[color.NRGBA]lookup)
	var opaque, unknown int
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			if c.A == 0 {
				g.Data[y*g.W+x] = MinDBZ
				continue
			}
			opaque++
			l, ok := cache[c]
			if !ok {
				l.dbz, l.exact = p.Lookup(c)
				cache[c] = l
			}
			if !l.exact {
				unknown++
			}
			g.Data[y*g.W+x] = l.dbz
		}
	}
	if opaque > 0 && float64(unknown) > maxUnknownFrac*float64(opaque) {
		return nil, ErrNotRadar
	}
	return g, nil
}

// TileKey identifies a tile in a mosaic.
type TileKey struct{ X, Y int }

// BuildMosaic stitches an n×n block of tiles whose top-left is (x0, y0).
// Missing tiles are left as MinDBZ.
func BuildMosaic(tiles map[TileKey][]byte, zoom, x0, y0, n int, p *Palette) (*Mosaic, error) {
	m := &Mosaic{Grid: NewGrid(n*tileSize, n*tileSize), Zoom: zoom, TileX: x0, TileY: y0}
	for ty := 0; ty < n; ty++ {
		for tx := 0; tx < n; tx++ {
			data, ok := tiles[TileKey{x0 + tx, y0 + ty}]
			if !ok {
				continue
			}
			t, err := Decode(data, p)
			if err != nil {
				return nil, fmt.Errorf("tile %d/%d: %w", x0+tx, y0+ty, err)
			}
			if t.W != tileSize || t.H != tileSize {
				return nil, fmt.Errorf("tile %d/%d: size %dx%d: %w", x0+tx, y0+ty, t.W, t.H, ErrNotRadar)
			}
			for y := 0; y < tileSize; y++ {
				copy(m.Data[(ty*tileSize+y)*m.W+tx*tileSize:], t.Data[y*tileSize:(y+1)*tileSize])
			}
		}
	}
	return m, nil
}

// Render draws g with the palette over a solid background.
func Render(g *Grid, p *Palette, bg color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, g.W, g.H))
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			img.SetNRGBA(x, y, blend(bg, p.Color(g.Data[y*g.W+x])))
		}
	}
	return img
}

func blend(bg, fg color.NRGBA) color.NRGBA {
	a := uint32(fg.A)
	mix := func(b, f uint8) uint8 { return uint8((uint32(f)*a + uint32(b)*(255-a)) / 255) }
	return color.NRGBA{mix(bg.R, fg.R), mix(bg.G, fg.G), mix(bg.B, fg.B), 255}
}
