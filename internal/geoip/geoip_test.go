package geoip

import (
	"net/netip"
	"path/filepath"
	"testing"
)

func TestMissingDatabase(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "none.mmdb"), nil)
	if err == nil {
		t.Fatal("want an error for a missing file")
	}
	// The DB still answers, knowing no countries until the file appears.
	if cc := d.Country(netip.MustParseAddr("8.8.8.8")); cc != "" {
		t.Fatalf("country = %q", cc)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	var none *DB
	if cc := none.Country(netip.MustParseAddr("8.8.8.8")); cc != "" {
		t.Fatalf("nil DB: country = %q", cc)
	}
}
