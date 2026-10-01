package server

import (
	"image"
	"image/color"
	"math"

	"raincast/internal/pipeline"
	"raincast/internal/radar"
)

var (
	bgColor     = color.NRGBA{15, 23, 42, 255}
	gridColor   = color.NRGBA{51, 65, 85, 255}
	ringColor   = color.NRGBA{148, 163, 184, 255}
	arrowColor  = color.NRGBA{255, 255, 255, 200}
	targetColor = color.NRGBA{239, 68, 68, 255}
)

// renderSnapshot draws the radar frame with tile grid, range rings around
// the target, motion arrows (displacement over 60 minutes) and the target.
func renderSnapshot(s *pipeline.Snapshot) *image.NRGBA {
	img := radar.Render(s.Mosaic.Grid, radar.DefaultPalette, bgColor)
	b := img.Bounds()

	for v := 256; v < b.Dx(); v += 256 {
		line(img, float64(v), 0, float64(v), float64(b.Dy()), gridColor)
	}
	for v := 256; v < b.Dy(); v += 256 {
		line(img, 0, float64(v), float64(b.Dx()), float64(v), gridColor)
	}

	if s.KmPerPx > 0 {
		for _, km := range []float64{25, 50, 100} {
			circle(img, s.X, s.Y, km/s.KmPerPx, ringColor)
		}
	}

	if f := s.Field; f != nil && f.Reliable() {
		step := f.BlockSize * 2
		for y := step / 2; y < b.Dy(); y += step {
			for x := step / 2; x < b.Dx(); x += step {
				v := f.At(float64(x), float64(y))
				arrow(img, float64(x), float64(y), v.DX*60, v.DY*60, arrowColor)
			}
		}
	}

	disc(img, s.X, s.Y, 5, targetColor)
	return img
}

func set(img *image.NRGBA, x, y int, c color.NRGBA) {
	if image.Pt(x, y).In(img.Bounds()) {
		img.SetNRGBA(x, y, c)
	}
}

func line(img *image.NRGBA, x0, y0, x1, y1 float64, c color.NRGBA) {
	n := int(math.Max(math.Abs(x1-x0), math.Abs(y1-y0))) + 1
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		set(img, int(math.Round(x0+(x1-x0)*t)), int(math.Round(y0+(y1-y0)*t)), c)
	}
}

func arrow(img *image.NRGBA, x, y, dx, dy float64, c color.NRGBA) {
	l := math.Hypot(dx, dy)
	if l < 1 {
		set(img, int(x), int(y), c)
		return
	}
	line(img, x, y, x+dx, y+dy, c)
	ang := math.Atan2(dy, dx)
	head := math.Min(6, l/2)
	for _, a := range []float64{ang + 2.6, ang - 2.6} {
		line(img, x+dx, y+dy, x+dx+head*math.Cos(a), y+dy+head*math.Sin(a), c)
	}
}

func circle(img *image.NRGBA, cx, cy, r float64, c color.NRGBA) {
	n := int(2*math.Pi*r) + 1
	for i := 0; i < n; i++ {
		a := 2 * math.Pi * float64(i) / float64(n)
		set(img, int(math.Round(cx+r*math.Cos(a))), int(math.Round(cy+r*math.Sin(a))), c)
	}
}

func disc(img *image.NRGBA, cx, cy float64, r int, c color.NRGBA) {
	for dy := -r - 1; dy <= r+1; dy++ {
		for dx := -r - 1; dx <= r+1; dx++ {
			d := math.Hypot(float64(dx), float64(dy))
			switch {
			case d <= float64(r):
				set(img, int(cx)+dx, int(cy)+dy, c)
			case d <= float64(r)+1.2:
				set(img, int(cx)+dx, int(cy)+dy, color.NRGBA{255, 255, 255, 255})
			}
		}
	}
}
