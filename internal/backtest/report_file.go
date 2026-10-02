package backtest

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// SaveReport writes rep as JSON atomically, for the admin page to read.
func SaveReport(path string, rep Report) error {
	js, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".backtest-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(js); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadReport reads a report saved by cmd/backtest, or nil when there is
// none yet.
func ReadReport(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rep Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, err
	}
	return &rep, nil
}
