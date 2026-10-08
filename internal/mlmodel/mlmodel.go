// Package mlmodel embeds the ML bundle the app forecasts with:
// internal/ml/models/radar.json from scripts/ml/train.py, gzipped (see
// COMMANDS.md to replace it). It is built into the binary so serverless
// deploys, which only ship the repository's code, have it.
package mlmodel

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"

	"raincast/internal/ml"
)

//go:embed radar.json.gz
var radar []byte

// Load decodes the embedded bundle (~0.2 s on one core).
func Load() (*ml.Bundle, error) {
	zr, err := gzip.NewReader(bytes.NewReader(radar))
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	return ml.Parse(data)
}
