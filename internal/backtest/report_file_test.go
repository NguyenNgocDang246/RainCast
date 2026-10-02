package backtest

import (
	"path/filepath"
	"testing"
)

func TestSaveAndReadReport(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bt.json")
	if rep, err := ReadReport(file); err != nil || rep != nil {
		t.Fatalf("no file: %+v %v", rep, err)
	}
	if err := SaveReport(file, Report{Issues: 9, Events: 31}); err != nil {
		t.Fatal(err)
	}
	rep, err := ReadReport(file)
	if err != nil || rep == nil || rep.Issues != 9 || rep.Events != 31 {
		t.Fatalf("read back %+v %v", rep, err)
	}
}
