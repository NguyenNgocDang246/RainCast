package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"), 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestVerifyPerStation(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	const t0 = 6000

	if _, err := s.RecordFrame(ctx, t0, "/p0", map[string]float64{"a": -32, "b": -32}, 1); err != nil {
		t.Fatal(err)
	}
	for _, st := range []string{"a", "b"} {
		if err := s.SaveIssue(ctx, st, t0, 8, false, []byte(`{}`),
			[]Forecast{{LeadMin: 10, PredDBZ: 30, PredRain: true}, {LeadMin: 20, PredDBZ: 5}}); err != nil {
			t.Fatal(err)
		}
	}
	// Rain arrives at a but not b.
	n, err := s.RecordFrame(ctx, t0+600, "/p1", map[string]float64{"a": 35, "b": 5}, 2)
	if err != nil || n != 2 {
		t.Fatalf("verified %d, err %v", n, err)
	}

	rows, err := s.VerifiedRows(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v %v", rows, err)
	}
	var hits, falseAlarms int
	for _, r := range rows {
		if r.PredRain && r.ObsRain {
			hits++
		}
		if r.PredRain && !r.ObsRain {
			falseAlarms++
		}
	}
	if hits != 1 || falseAlarms != 1 {
		t.Fatalf("hits=%d false alarms=%d", hits, falseAlarms)
	}

	if ok, _ := s.HasIssue(ctx, "a", t0); !ok {
		t.Fatal("HasIssue(a) false")
	}
	if ok, _ := s.HasIssue(ctx, "c", t0); ok {
		t.Fatal("HasIssue(c) true")
	}
	if ok, _ := s.HasFrame(ctx, t0+600); !ok {
		t.Fatal("HasFrame false")
	}
	rain, err := s.StationRain(ctx)
	if err != nil || !rain["a"][t0+600] || rain["b"][t0+600] {
		t.Fatalf("rain = %v %v", rain, err)
	}
	issues, err := s.StationIssues(ctx)
	if err != nil || len(issues["a"]) != 1 || issues["b"][0].ArrivalMin != 8 {
		t.Fatalf("issues = %+v %v", issues, err)
	}
}

func TestVerifyExistingObservation(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	// Target frame observed before the forecast is saved (restart/backfill).
	if _, err := s.RecordFrame(ctx, 1200, "/p", map[string]float64{"a": 10}, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIssue(ctx, "a", 600, -1, false, []byte(`{}`), []Forecast{{LeadMin: 10, PredDBZ: 25, PredRain: true}}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.VerifiedRows(ctx)
	if err != nil || len(rows) != 1 || rows[0].ObsRain || rows[0].ObsDBZ != 10 {
		t.Fatalf("rows = %+v %v", rows, err)
	}
}

// A database written by the single-station version keeps its data, assigned
// to the legacy station.
func TestMigrateFromSingleStation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE frames (time INTEGER PRIMARY KEY, path TEXT NOT NULL, obs_dbz REAL NOT NULL, recorded_at INTEGER NOT NULL);
		CREATE TABLE issues (issued_at INTEGER PRIMARY KEY, arrival_min INTEGER NOT NULL, raining_now INTEGER NOT NULL, result_json TEXT NOT NULL);
		CREATE TABLE forecasts (issued_at INTEGER NOT NULL, lead_min INTEGER NOT NULL, pred_dbz REAL NOT NULL, pred_rain INTEGER NOT NULL,
			persist_dbz REAL NOT NULL, persist_rain INTEGER NOT NULL, obs_dbz REAL, obs_rain INTEGER, PRIMARY KEY (issued_at, lead_min));
		CREATE INDEX forecasts_target ON forecasts (issued_at + lead_min * 60);
		INSERT INTO frames VALUES (600, '/a', 25, 1), (1200, '/b', 30, 2);
		INSERT INTO issues VALUES (600, 5, 1, '{}');
		INSERT INTO forecasts VALUES (600, 10, 28, 1, 25, 1, 30, 1), (600, 20, 22, 1, 25, 1, NULL, NULL);
	`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rain, err := s.StationRain(ctx)
	if err != nil || len(rain[LegacyStation]) != 2 {
		t.Fatalf("rain = %v %v", rain, err)
	}
	if ok, _ := s.HasIssue(ctx, LegacyStation, 600); !ok {
		t.Fatal("issue not migrated")
	}
	rows, err := s.VerifiedRows(ctx)
	if err != nil || len(rows) != 1 || rows[0].ObsDBZ != 30 {
		t.Fatalf("rows = %+v %v", rows, err)
	}
	// The pending lead still verifies against new observations.
	if n, err := s.RecordFrame(ctx, 1800, "/c", map[string]float64{LegacyStation: 40}, 3); err != nil || n != 1 {
		t.Fatalf("verified %d %v", n, err)
	}
	// Reopening an up-to-date database is a no-op.
	s.Close()
	s, err = Open(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func TestObservedStations(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, err := s.RecordFrame(ctx, 600, "/p", map[string]float64{"a": 1}, 1); err != nil {
		t.Fatal(err)
	}
	// A station added later observes the same frame without clobbering a.
	if _, err := s.RecordFrame(ctx, 600, "/p", map[string]float64{"b": 2}, 2); err != nil {
		t.Fatal(err)
	}
	got, err := s.ObservedStations(ctx, 600)
	if err != nil || !got["a"] || !got["b"] || len(got) != 2 {
		t.Fatalf("observed = %v %v", got, err)
	}
}

func TestMigrateV2AddsTrendColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE frames (time INTEGER PRIMARY KEY, path TEXT NOT NULL, recorded_at INTEGER NOT NULL);
		CREATE TABLE observations (station TEXT NOT NULL, time INTEGER NOT NULL, obs_dbz REAL NOT NULL, PRIMARY KEY (station, time));
		CREATE TABLE issues (station TEXT NOT NULL, issued_at INTEGER NOT NULL, arrival_min INTEGER NOT NULL,
			raining_now INTEGER NOT NULL, result_json TEXT NOT NULL, PRIMARY KEY (station, issued_at));
		CREATE TABLE forecasts (station TEXT NOT NULL, issued_at INTEGER NOT NULL, lead_min INTEGER NOT NULL,
			pred_dbz REAL NOT NULL, pred_rain INTEGER NOT NULL, persist_dbz REAL NOT NULL, persist_rain INTEGER NOT NULL,
			obs_dbz REAL, obs_rain INTEGER, PRIMARY KEY (station, issued_at, lead_min));
		INSERT INTO forecasts VALUES ('a', 600, 10, 25, 1, 25, 1, 30, 1);
		PRAGMA user_version = 2;
	`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.SaveIssue(ctx, "a", 1200, -1, false, []byte(`{}`), []Forecast{
		{LeadMin: 10, PredDBZ: 15, Trend: &TrendForecast{DBZ: 24, Rain: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordFrame(ctx, 1800, "/p", map[string]float64{"a": 26}, 1); err != nil {
		t.Fatal(err)
	}
	rows, err := s.VerifiedRows(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v %v", rows, err)
	}
	for _, r := range rows {
		switch r.IssuedAt {
		case 600:
			if r.Trend != nil {
				t.Errorf("old row has trend %+v", r.Trend)
			}
		case 1200:
			if r.Trend == nil || !r.Trend.Rain || r.Trend.DBZ != 24 {
				t.Errorf("new row trend = %+v", r.Trend)
			}
		}
	}
}
