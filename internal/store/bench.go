package store

import (
	"context"
	"time"
)

// BenchForecast is one rain rate a provider forecast for a station:
// issued at Issued for Valid (unix s). Issued == Valid is the provider's
// own "now", which the benchmark takes as that provider's observation.
type BenchForecast struct {
	Provider string  `json:"provider"`
	Station  string  `json:"station"`
	Issued   int64   `json:"issued_at"`
	Valid    int64   `json:"valid_at"`
	MMH      float64 `json:"mm_h"`
	Kind     string  `json:"kind"` // how the provider made it (bench.Kind*)
}

// BenchObs is the radar echo RainViewer showed at a station in a frame.
type BenchObs struct {
	Station string  `json:"station"`
	Frame   int64   `json:"frame"` // unix s
	DBZ     float64 `json:"dbz"`
}

// BenchMetar is an airport's METAR: whether precipitation was reported.
type BenchMetar struct {
	Station string `json:"station"` // ICAO code
	Obs     int64  `json:"obs_time"` // unix s
	Rain    bool   `json:"rain"`
	Raw     string `json:"raw"`
}

// InsertBenchForecasts stores rows; a row already stored is kept.
func (s *Store) InsertBenchForecasts(ctx context.Context, rows []BenchForecast) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bench_forecasts (provider, station, issued_at, valid_at, mm_h, kind)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`,
			r.Provider, r.Station, r.Issued, r.Valid, r.MMH, r.Kind); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RecordBenchObs stores o; a frame already recorded for the station is kept.
func (s *Store) RecordBenchObs(ctx context.Context, o BenchObs) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO bench_obs (station, frame, dbz) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		o.Station, o.Frame, o.DBZ)
	return err
}

// InsertBenchMetar stores reports; a report already stored is kept.
func (s *Store) InsertBenchMetar(ctx context.Context, reps []BenchMetar) error {
	if len(reps) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range reps {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bench_metar (station, obs_time, rain, raw) VALUES ($1, $2, $3, $4)
			ON CONFLICT DO NOTHING`, r.Station, r.Obs, r.Rain, r.Raw); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BenchMetars returns every METAR from since (unix s) on.
func (s *Store) BenchMetars(ctx context.Context, since int64) ([]BenchMetar, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT station, obs_time, rain, raw FROM bench_metar WHERE obs_time >= $1 ORDER BY obs_time`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BenchMetar{}
	for rows.Next() {
		var r BenchMetar
		if err := rows.Scan(&r.Station, &r.Obs, &r.Rain, &r.Raw); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// BenchForecasts returns every forecast valid from since (unix s) on.
func (s *Store) BenchForecasts(ctx context.Context, since int64) ([]BenchForecast, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, station, issued_at, valid_at, mm_h, kind FROM bench_forecasts
		WHERE valid_at >= $1 ORDER BY issued_at, valid_at`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BenchForecast{}
	for rows.Next() {
		var r BenchForecast
		if err := rows.Scan(&r.Provider, &r.Station, &r.Issued, &r.Valid, &r.MMH, &r.Kind); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// BenchObservations returns every radar observation from since (unix s) on.
func (s *Store) BenchObservations(ctx context.Context, since int64) ([]BenchObs, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT station, frame, dbz FROM bench_obs WHERE frame >= $1 ORDER BY frame`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BenchObs{}
	for rows.Next() {
		var o BenchObs
		if err := rows.Scan(&o.Station, &o.Frame, &o.DBZ); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// BenchCalls counts the successful calls to provider since t: one per
// station and issue time.
func (s *Store) BenchCalls(ctx context.Context, provider string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (SELECT DISTINCT station, issued_at FROM bench_forecasts
		WHERE provider = $1 AND issued_at >= $2) c`, provider, since.Unix()).Scan(&n)
	return n, err
}
