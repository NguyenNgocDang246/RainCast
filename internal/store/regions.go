package store

import (
	"context"
)

// regionSchema is created on every open, so it needs no migration step.
const regionSchema = `
CREATE TABLE IF NOT EXISTS regions (
	tile_x       INTEGER NOT NULL,
	tile_y       INTEGER NOT NULL,
	lat          REAL    NOT NULL,
	lon          REAL    NOT NULL,
	climate      TEXT    NOT NULL,
	first_seen   INTEGER NOT NULL,
	last_active  INTEGER NOT NULL,
	PRIMARY KEY (tile_x, tile_y)
);
`

// Region is a radar area collected for backtesting: the 3×3 z7 tiles around
// (TileX, TileY), with tiles cached between FirstSeen and LastActive.
type Region struct {
	TileX      int     `json:"tile_x"`
	TileY      int     `json:"tile_y"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	Climate    string  `json:"climate"`
	FirstSeen  int64   `json:"first_seen"`
	LastActive int64   `json:"last_active"`
}

// TouchRegion records that r was collected at time now; first_seen stays
// the time of the first touch.
func (s *Store) TouchRegion(ctx context.Context, r Region, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO regions (tile_x, tile_y, lat, lon, climate, first_seen, last_active)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tile_x, tile_y) DO UPDATE SET last_active = MAX(last_active, excluded.last_active)`,
		r.TileX, r.TileY, r.Lat, r.Lon, r.Climate, now, now)
	return err
}

// Regions lists every collected region.
func (s *Store) Regions(ctx context.Context) ([]Region, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tile_x, tile_y, lat, lon, climate, first_seen, last_active FROM regions ORDER BY first_seen`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Region{}
	for rows.Next() {
		var r Region
		if err := rows.Scan(&r.TileX, &r.TileY, &r.Lat, &r.Lon, &r.Climate, &r.FirstSeen, &r.LastActive); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FrameRef is a recorded frame's time and tile path.
type FrameRef struct {
	Time int64
	Path string
}

// FrameList returns every recorded frame since from (unix s), oldest first.
func (s *Store) FrameList(ctx context.Context, from int64) ([]FrameRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT time, path FROM frames WHERE time >= ? ORDER BY time`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FrameRef{}
	for rows.Next() {
		var f FrameRef
		if err := rows.Scan(&f.Time, &f.Path); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RecordFrameOnly stores frame t without observations, for frames seen by
// the collector before any station recorded them.
func (s *Store) RecordFrameOnly(ctx context.Context, t int64, path string, now int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO frames (time, path, recorded_at) VALUES (?, ?, ?)`, t, path, now)
	return err
}
