// Package store persists radar frames, per-station forecasts and their
// verification in SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"raincast/internal/verify"
)

// schemaVersion is stored in PRAGMA user_version.
const schemaVersion = 3

// LegacyStation is the station that data from the single-station schema
// (version 1, Thủ Đức) is assigned to when migrating.
const LegacyStation = "thu-duc"

const schema = `
CREATE TABLE IF NOT EXISTS frames (
	time         INTEGER PRIMARY KEY,
	path         TEXT    NOT NULL,
	recorded_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS observations (
	station      TEXT    NOT NULL,
	time         INTEGER NOT NULL,
	obs_dbz      REAL    NOT NULL,
	PRIMARY KEY (station, time)
);
CREATE TABLE IF NOT EXISTS issues (
	station      TEXT    NOT NULL,
	issued_at    INTEGER NOT NULL,
	arrival_min  INTEGER NOT NULL,
	raining_now  INTEGER NOT NULL,
	result_json  TEXT    NOT NULL,
	PRIMARY KEY (station, issued_at)
);
CREATE TABLE IF NOT EXISTS forecasts (
	station      TEXT    NOT NULL,
	issued_at    INTEGER NOT NULL,
	lead_min     INTEGER NOT NULL,
	pred_dbz     REAL    NOT NULL,
	pred_rain    INTEGER NOT NULL,
	persist_dbz  REAL    NOT NULL,
	persist_rain INTEGER NOT NULL,
	trend_dbz    REAL,    -- shadow forecast with intensity trend; NULL before v3
	trend_rain   INTEGER,
	obs_dbz      REAL,
	obs_rain     INTEGER,
	PRIMARY KEY (station, issued_at, lead_min)
);
CREATE INDEX IF NOT EXISTS forecasts_target ON forecasts (station, issued_at + lead_min * 60);
`

// migrateV1 moves single-station tables to the per-station layout.
const migrateV1 = `
ALTER TABLE frames RENAME TO frames_v1;
ALTER TABLE issues RENAME TO issues_v1;
ALTER TABLE forecasts RENAME TO forecasts_v1;
DROP INDEX IF EXISTS forecasts_target;
` + schema + `
INSERT INTO frames (time, path, recorded_at) SELECT time, path, recorded_at FROM frames_v1;
INSERT INTO observations (station, time, obs_dbz) SELECT '` + LegacyStation + `', time, obs_dbz FROM frames_v1;
INSERT INTO issues SELECT '` + LegacyStation + `', issued_at, arrival_min, raining_now, result_json FROM issues_v1;
INSERT INTO forecasts (station, issued_at, lead_min, pred_dbz, pred_rain, persist_dbz, persist_rain, obs_dbz, obs_rain)
	SELECT '` + LegacyStation + `', issued_at, lead_min, pred_dbz, pred_rain, persist_dbz, persist_rain, obs_dbz, obs_rain FROM forecasts_v1;
DROP TABLE frames_v1;
DROP TABLE issues_v1;
DROP TABLE forecasts_v1;
`

// Store wraps the SQLite database.
type Store struct {
	db        *sql.DB
	threshold float64
}

// Open creates or opens the database at path, migrating older schemas.
// threshold is the dBZ at which an observation counts as rain.
func Open(path string, threshold float64) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; keeps WAL contention out of the picture
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &Store{db: db, threshold: threshold}, nil
}

// migrateV2 adds the shadow trend forecast to each lead.
const migrateV2 = `
ALTER TABLE forecasts ADD COLUMN trend_dbz REAL;
ALTER TABLE forecasts ADD COLUMN trend_rain INTEGER;
`

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= schemaVersion {
		_, err := db.Exec(schema + adminSchema + regionSchema)
		return err
	}
	var steps string
	switch version {
	case 0:
		// A new file, or the single-station layout (obs_dbz on frames).
		var legacy int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('frames') WHERE name = 'obs_dbz'`).Scan(&legacy); err != nil {
			return err
		}
		steps = schema
		if legacy > 0 {
			steps = migrateV1
		}
	case 2:
		steps = migrateV2 + schema
	default:
		return fmt.Errorf("unknown schema version %d", version)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(steps + adminSchema + regionSchema); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// RecordFrame stores frame t with the echo observed at each station, and
// verifies every forecast that targeted it. It returns how many were verified.
func (s *Store) RecordFrame(ctx context.Context, t int64, path string, obs map[string]float64, now int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO frames (time, path, recorded_at) VALUES (?, ?, ?)`, t, path, now); err != nil {
		return 0, err
	}
	var verified int64
	for station, dbz := range obs {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO observations (station, time, obs_dbz) VALUES (?, ?, ?)`, station, t, dbz); err != nil {
			return 0, err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE forecasts SET obs_dbz = ?, obs_rain = ?
			WHERE station = ? AND issued_at + lead_min * 60 = ? AND obs_dbz IS NULL`,
			dbz, dbz >= s.threshold, station, t)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		verified += n
	}
	return verified, tx.Commit()
}

// HasFrame reports whether frame t was already recorded.
func (s *Store) HasFrame(ctx context.Context, t int64) (bool, error) {
	return s.exists(ctx, `SELECT 1 FROM frames WHERE time = ?`, t)
}

// ObservedStations returns the stations that already have an observation
// for frame t.
func (s *Store) ObservedStations(ctx context.Context, t int64) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT station FROM observations WHERE time = ?`, t)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			return nil, err
		}
		out[st] = true
	}
	return out, rows.Err()
}

// HasIssue reports whether station already issued a forecast from frame t.
func (s *Store) HasIssue(ctx context.Context, station string, t int64) (bool, error) {
	return s.exists(ctx, `SELECT 1 FROM issues WHERE station = ? AND issued_at = ?`, station, t)
}

func (s *Store) exists(ctx context.Context, query string, args ...any) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Forecast is one lead-time prediction to be verified later.
type Forecast struct {
	LeadMin     int
	PredDBZ     float64
	PersistDBZ  float64
	PredRain    bool
	PersistRain bool
	// Trend is the shadow forecast with intensity growth/decay, or nil.
	Trend *TrendForecast
}

// TrendForecast is the shadow prediction for one lead.
type TrendForecast struct {
	DBZ  float64
	Rain bool
}

// SaveIssue stores a station's forecast issued from frame issuedAt. Leads
// whose target frame is already observed are verified immediately.
func (s *Store) SaveIssue(ctx context.Context, station string, issuedAt int64, arrivalMin int, rainingNow bool, resultJSON []byte, leads []Forecast) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO issues (station, issued_at, arrival_min, raining_now, result_json)
		VALUES (?, ?, ?, ?, ?)`,
		station, issuedAt, arrivalMin, rainingNow, string(resultJSON)); err != nil {
		return err
	}
	for _, f := range leads {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO forecasts
				(station, issued_at, lead_min, pred_dbz, pred_rain, persist_dbz, persist_rain, trend_dbz, trend_rain)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			station, issuedAt, f.LeadMin, f.PredDBZ, f.PredRain, f.PersistDBZ, f.PersistRain,
			trendDBZ(f.Trend), trendRain(f.Trend)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE forecasts
		SET obs_dbz  = (SELECT o.obs_dbz FROM observations o
		                WHERE o.station = forecasts.station AND o.time = forecasts.issued_at + forecasts.lead_min * 60),
		    obs_rain = (SELECT o.obs_dbz >= ? FROM observations o
		                WHERE o.station = forecasts.station AND o.time = forecasts.issued_at + forecasts.lead_min * 60)
		WHERE station = ? AND issued_at = ? AND obs_dbz IS NULL
		  AND EXISTS (SELECT 1 FROM observations o
		              WHERE o.station = forecasts.station AND o.time = forecasts.issued_at + forecasts.lead_min * 60)`,
		s.threshold, station, issuedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// VerifiedRows returns every forecast, across all stations, that has an
// observation.
func (s *Store) VerifiedRows(ctx context.Context) ([]verify.Row, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT issued_at, lead_min, pred_rain, persist_rain, obs_rain, pred_dbz, obs_dbz, trend_dbz, trend_rain
		FROM forecasts WHERE obs_dbz IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []verify.Row
	for rows.Next() {
		var r verify.Row
		var tDBZ sql.NullFloat64
		var tRain sql.NullBool
		if err := rows.Scan(&r.IssuedAt, &r.LeadMin, &r.PredRain, &r.PersistRain, &r.ObsRain, &r.PredDBZ, &r.ObsDBZ, &tDBZ, &tRain); err != nil {
			return nil, err
		}
		if tDBZ.Valid {
			r.Trend = &verify.Prediction{DBZ: tDBZ.Float64, Rain: tRain.Bool}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StationIssues groups every issued forecast's headline by station.
func (s *Store) StationIssues(ctx context.Context) (map[string][]verify.Issue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT station, issued_at, arrival_min, raining_now FROM issues`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]verify.Issue{}
	for rows.Next() {
		var st string
		var is verify.Issue
		if err := rows.Scan(&st, &is.IssuedAt, &is.ArrivalMin, &is.RainingNow); err != nil {
			return nil, err
		}
		out[st] = append(out[st], is)
	}
	return out, rows.Err()
}

// StationRain maps, per station, every observed frame time to whether it
// showed rain.
func (s *Store) StationRain(ctx context.Context) (map[string]map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT station, time, obs_dbz >= ? FROM observations`, s.threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[int64]bool{}
	for rows.Next() {
		var st string
		var t int64
		var rain bool
		if err := rows.Scan(&st, &t, &rain); err != nil {
			return nil, err
		}
		if out[st] == nil {
			out[st] = map[int64]bool{}
		}
		out[st][t] = rain
	}
	return out, rows.Err()
}

func trendDBZ(t *TrendForecast) any {
	if t == nil {
		return nil
	}
	return t.DBZ
}

func trendRain(t *TrendForecast) any {
	if t == nil {
		return nil
	}
	return t.Rain
}
