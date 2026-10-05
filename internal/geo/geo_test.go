package geo

import (
	"math"
	"testing"
)

func TestThuDucTile(t *testing.T) {
	x, y := LatLonToPixel(10.85, 106.77, 7)
	tx, ty := TileOf(x, y)
	if tx != 101 || ty != 60 {
		t.Fatalf("tile = (%d,%d), want (101,60)", tx, ty)
	}
}

func TestRoundTrip(t *testing.T) {
	for _, c := range [][2]float64{{10.85, 106.77}, {0, 0}, {-33.9, 151.2}, {60, -120}} {
		x, y := LatLonToPixel(c[0], c[1], 7)
		lat, lon := PixelToLatLon(x, y, 7)
		if math.Abs(lat-c[0]) > 1e-9 || math.Abs(lon-c[1]) > 1e-9 {
			t.Errorf("round trip %v -> (%f,%f)", c, lat, lon)
		}
	}
}

// The center of pixel (i, j) maps to index (i, j); a point inside it rounds
// to it.
func TestLatLonToIndex(t *testing.T) {
	lat, lon := PixelToLatLon(25856+0.5, 15386+0.5, 7)
	x, y := LatLonToIndex(lat, lon, 7)
	if math.Abs(x-25856) > 1e-6 || math.Abs(y-15386) > 1e-6 {
		t.Fatalf("center -> (%f,%f), want (25856,15386)", x, y)
	}
	lat, lon = PixelToLatLon(25856+0.9, 15386+0.9, 7)
	x, y = LatLonToIndex(lat, lon, 7)
	if math.Round(x) != 25856 || math.Round(y) != 15386 {
		t.Fatalf("inside -> (%f,%f), want to round to (25856,15386)", x, y)
	}
}

func TestMetersPerPixel(t *testing.T) {
	m := MetersPerPixel(10.85, 7)
	if m < 1150 || m > 1250 {
		t.Fatalf("meters/pixel = %f, want ~1200", m)
	}
}
