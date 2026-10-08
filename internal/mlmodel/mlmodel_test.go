package mlmodel

import (
	"testing"

	"raincast/internal/ml"
)

// The embedded bundle loads and reads only features the app computes:
// radar ones, no NWP or satellite.
func TestLoad(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if b.Set != ml.SetRadar || b.Uses(ml.PrefixNWP) || b.Uses(ml.PrefixSat) {
		t.Fatalf("set %q reads NWP %v, satellite %v", b.Set, b.Uses(ml.PrefixNWP), b.Uses(ml.PrefixSat))
	}
}
