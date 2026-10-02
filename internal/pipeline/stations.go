package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadStations reads and validates a stations file:
// [{"id","name","lat","lon"}, …], the first being the primary station.
func LoadStations(path string) ([]Station, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st []Station
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(st) == 0 {
		return nil, fmt.Errorf("%s: no stations", path)
	}
	seen := map[string]bool{}
	for _, s := range st {
		if s.ID == "" || seen[s.ID] {
			return nil, fmt.Errorf("%s: station ids must be unique and non-empty (%q)", path, s.ID)
		}
		if s.Lat < -85 || s.Lat > 85 || s.Lon < -180 || s.Lon > 180 {
			return nil, fmt.Errorf("%s: station %q has invalid coordinates", path, s.ID)
		}
		seen[s.ID] = true
	}
	return st, nil
}
