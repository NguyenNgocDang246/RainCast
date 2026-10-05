package store

import (
	"context"
)

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
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tile_x, tile_y) DO UPDATE SET last_active = GREATEST(regions.last_active, excluded.last_active)`,
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

// FrameRef is a recorded frame's time (unix s) and tile path.
type FrameRef struct {
	Time int64  `json:"time"`
	Path string `json:"path"`
}

// FrameList returns every recorded frame since from (unix s), oldest first.
func (s *Store) FrameList(ctx context.Context, from int64) ([]FrameRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT time, path FROM frames WHERE time >= $1 ORDER BY time`, from)
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

// RecordFrame stores frame t; a frame already recorded is kept.
func (s *Store) RecordFrame(ctx context.Context, t int64, path string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO frames (time, path) VALUES ($1, $2) ON CONFLICT DO NOTHING`, t, path)
	return err
}

// DeleteFrames removes the frames recorded at times.
func (s *Store) DeleteFrames(ctx context.Context, times []int64) error {
	for _, t := range times {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM frames WHERE time = $1`, t); err != nil {
			return err
		}
	}
	return nil
}

// DeleteRegion removes the region at (tileX, tileY).
func (s *Store) DeleteRegion(ctx context.Context, tileX, tileY int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM regions WHERE tile_x = $1 AND tile_y = $2`, tileX, tileY)
	return err
}

// RecentFrames returns the latest recorded frames, newest first.
func (s *Store) RecentFrames(ctx context.Context, limit int) ([]FrameRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT time, path FROM frames ORDER BY time DESC LIMIT $1`, limit)
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

// Counts summarizes what was collected.
type Counts struct {
	Frames  int `json:"frames"`
	Regions int `json:"regions"`
}

// Counts returns the number of frames and regions.
func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM frames), (SELECT COUNT(*) FROM regions)`).
		Scan(&c.Frames, &c.Regions)
	return c, err
}
