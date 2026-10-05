package radar

import "math"

// Z–R relation Z = a·R^b (Z in mm⁶/m³, R in mm/h). Marshall–Palmer suits
// stratiform rain; convective tropical rain is usually wetter for the same
// echo, so these estimates lean low in thunderstorms.
const (
	zrA = 200.0
	zrB = 1.6
	// Below qpeMinDBZ echoes are drizzle or clutter that rarely reaches the
	// ground; above qpeMaxDBZ they are mostly hail, which would wildly
	// overstate rain.
	qpeMinDBZ = 15
	qpeMaxDBZ = 55
)

// RainRate converts reflectivity to rain rate in mm/h.
func RainRate(dbz float32) float64 {
	if dbz < qpeMinDBZ {
		return 0
	}
	d := math.Min(float64(dbz), qpeMaxDBZ)
	return math.Pow(math.Pow(10, d/10)/zrA, 1/zrB)
}

// DBZ is the reflectivity RainRate turns into mm/h rain, its inverse
// between qpeMinDBZ and qpeMaxDBZ.
func DBZ(mmh float64) float32 {
	return float32(10 * math.Log10(zrA*math.Pow(mmh, zrB)))
}
