// Package geo converts between WGS84 coordinates and Web Mercator tile pixels.
package geo

import "math"

// TileSize is the edge length of a map tile in pixels.
const TileSize = 256

// earthCircumference is the equatorial circumference in meters (WGS84).
const earthCircumference = 2 * math.Pi * 6378137

// worldSize returns the width of the world in pixels at zoom z.
func worldSize(z int) float64 {
	return float64(TileSize) * math.Exp2(float64(z))
}

// LatLonToPixel returns global pixel coordinates at zoom z.
func LatLonToPixel(lat, lon float64, z int) (x, y float64) {
	s := worldSize(z)
	x = (lon + 180) / 360 * s
	sin := math.Sin(lat * math.Pi / 180)
	y = (0.5 - math.Log((1+sin)/(1-sin))/(4*math.Pi)) * s
	return x, y
}

// LatLonToIndex returns global pixel coordinates at zoom z with pixel (i, j)
// centered on (i, j), as radar.Grid indexes them. LatLonToPixel puts pixel i
// over [i, i+1), so rounding its result picks the wrong pixel half the time.
func LatLonToIndex(lat, lon float64, z int) (x, y float64) {
	x, y = LatLonToPixel(lat, lon, z)
	return x - 0.5, y - 0.5
}

// PixelToLatLon is the inverse of LatLonToPixel.
func PixelToLatLon(x, y float64, z int) (lat, lon float64) {
	s := worldSize(z)
	lon = x/s*360 - 180
	n := math.Pi - 2*math.Pi*y/s
	lat = 180 / math.Pi * math.Atan(math.Sinh(n))
	return lat, lon
}

// TileOf returns the tile containing global pixel (x, y).
func TileOf(x, y float64) (tx, ty int) {
	return int(math.Floor(x / TileSize)), int(math.Floor(y / TileSize))
}

// MetersPerPixel returns the ground resolution at latitude lat and zoom z.
func MetersPerPixel(lat float64, z int) float64 {
	return earthCircumference * math.Cos(lat*math.Pi/180) / worldSize(z)
}
