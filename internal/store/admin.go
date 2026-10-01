package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

const adminSchema = `
CREATE TABLE IF NOT EXISTS lookups (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	at                INTEGER NOT NULL,
	lat               REAL    NOT NULL,
	lon               REAL    NOT NULL,
	frame_time        INTEGER NOT NULL,
	raining_now       INTEGER NOT NULL,
	arrival_min       INTEGER NOT NULL,
	heavy_now         INTEGER NOT NULL,
	heavy_arrival_min INTEGER NOT NULL,
	speed_kmh         REAL    NOT NULL,
	direction_deg     REAL    NOT NULL,
	UNIQUE (lat, lon, frame_time)
);
CREATE INDEX IF NOT EXISTS lookups_at ON lookups (at);
`

// Lookup is one on-demand forecast served to a user.
type Lookup struct {
	ID              int64     `json:"id"`
	At              time.Time `json:"at"`
	Lat             float64   `json:"lat"`
	Lon             float64   `json:"lon"`
	FrameTime       time.Time `json:"frame_time"`
	RainingNow      bool      `json:"raining_now"`
	ArrivalMin      int       `json:"arrival_min"`
	HeavyNow        bool      `json:"heavy_now"`
	HeavyArrivalMin int       `json:"heavy_arrival_min"`
	SpeedKmh        float64   `json:"speed_kmh"`
	DirectionDeg    float64   `json:"direction_deg"`
}

// RecordLookup stores an on-demand forecast. Repeats for the same place and
// radar frame (the page refreshes every minute) are ignored.
func (s *Store) RecordLookup(ctx context.Context, l Lookup) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO lookups
			(at, lat, lon, frame_time, raining_now, arrival_min, heavy_now, heavy_arrival_min, speed_kmh, direction_deg)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.At.Unix(), l.Lat, l.Lon, l.FrameTime.Unix(), l.RainingNow, l.ArrivalMin,
		l.HeavyNow, l.HeavyArrivalMin, l.SpeedKmh, l.DirectionDeg)
	return err
}

// Lookups returns the most recent on-demand forecasts.
func (s *Store) Lookups(ctx context.Context, limit int) ([]Lookup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, lat, lon, frame_time, raining_now, arrival_min, heavy_now, heavy_arrival_min, speed_kmh, direction_deg
		FROM lookups ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Lookup{}
	for rows.Next() {
		var l Lookup
		var at, frame int64
		if err := rows.Scan(&l.ID, &at, &l.Lat, &l.Lon, &frame, &l.RainingNow, &l.ArrivalMin,
			&l.HeavyNow, &l.HeavyArrivalMin, &l.SpeedKmh, &l.DirectionDeg); err != nil {
			return nil, err
		}
		l.At, l.FrameTime = time.Unix(at, 0).UTC(), time.Unix(frame, 0).UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

// LeadResult is one lead time of an issued forecast and, once the target
// frame has arrived, what was observed.
type LeadResult struct {
	LeadMin     int      `json:"lead_min"`
	PredDBZ     float64  `json:"pred_dbz"`
	PredRain    bool     `json:"pred_rain"`
	PersistRain bool     `json:"persist_rain"`
	ObsDBZ      *float64 `json:"obs_dbz"`  // nil until verified
	ObsRain     *bool    `json:"obs_rain"` // nil until verified
}

// IssueDetail is a station forecast with its per-lead outcomes.
type IssueDetail struct {
	Station    string          `json:"station"`
	IssuedAt   time.Time       `json:"issued_at"`
	ArrivalMin int             `json:"arrival_min"`
	RainingNow bool            `json:"raining_now"`
	Result     json.RawMessage `json:"result"` // the full forecast as served
	Leads      []LeadResult    `json:"leads"`
}

type issueKey struct {
	station string
	at      int64
}

// RecentIssues returns the latest forecasts of all stations, newest first.
func (s *Store) RecentIssues(ctx context.Context, limit int) ([]IssueDetail, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT station, issued_at, arrival_min, raining_now, result_json
		FROM issues ORDER BY issued_at DESC, station LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	out := []IssueDetail{}
	index := map[issueKey]int{}
	for rows.Next() {
		var d IssueDetail
		var at int64
		var js string
		if err := rows.Scan(&d.Station, &at, &d.ArrivalMin, &d.RainingNow, &js); err != nil {
			rows.Close()
			return nil, err
		}
		d.IssuedAt, d.Result, d.Leads = time.Unix(at, 0).UTC(), json.RawMessage(js), []LeadResult{}
		index[issueKey{d.Station, at}] = len(out)
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}

	oldest := out[len(out)-1].IssuedAt.Unix()
	lr, err := s.db.QueryContext(ctx, `
		SELECT station, issued_at, lead_min, pred_dbz, pred_rain, persist_rain, obs_dbz, obs_rain
		FROM forecasts WHERE issued_at >= ? ORDER BY issued_at, lead_min`, oldest)
	if err != nil {
		return nil, err
	}
	defer lr.Close()
	for lr.Next() {
		var st string
		var at int64
		var l LeadResult
		var obs sql.NullFloat64
		var obsRain sql.NullBool
		if err := lr.Scan(&st, &at, &l.LeadMin, &l.PredDBZ, &l.PredRain, &l.PersistRain, &obs, &obsRain); err != nil {
			return nil, err
		}
		if obs.Valid {
			l.ObsDBZ, l.ObsRain = &obs.Float64, &obsRain.Bool
		}
		if i, ok := index[issueKey{st, at}]; ok {
			out[i].Leads = append(out[i].Leads, l)
		}
	}
	return out, lr.Err()
}

// Frame is a recorded radar frame and the echo seen at each station.
type Frame struct {
	Time       time.Time          `json:"time"`
	Path       string             `json:"path"`
	Obs        map[string]float64 `json:"obs"` // station -> dBZ
	RecordedAt time.Time          `json:"recorded_at"`
}

// RecentFrames returns the latest recorded frames, newest first.
func (s *Store) RecentFrames(ctx context.Context, limit int) ([]Frame, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT time, path, recorded_at FROM frames ORDER BY time DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	out := []Frame{}
	index := map[int64]int{}
	for rows.Next() {
		f := Frame{Obs: map[string]float64{}}
		var t, rec int64
		if err := rows.Scan(&t, &f.Path, &rec); err != nil {
			rows.Close()
			return nil, err
		}
		f.Time, f.RecordedAt = time.Unix(t, 0).UTC(), time.Unix(rec, 0).UTC()
		index[t] = len(out)
		out = append(out, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	obs, err := s.db.QueryContext(ctx, `SELECT station, time, obs_dbz FROM observations WHERE time >= ?`,
		out[len(out)-1].Time.Unix())
	if err != nil {
		return nil, err
	}
	defer obs.Close()
	for obs.Next() {
		var st string
		var t int64
		var v float64
		if err := obs.Scan(&st, &t, &v); err != nil {
			return nil, err
		}
		if i, ok := index[t]; ok {
			out[i].Obs[st] = v
		}
	}
	return out, obs.Err()
}

// Counts summarizes table sizes.
type Counts struct {
	Frames    int `json:"frames"`
	Stations  int `json:"stations"`
	Issues    int `json:"issues"`
	Forecasts int `json:"forecasts"`
	Verified  int `json:"verified"`
	Lookups   int `json:"lookups"`
}

// Counts returns the number of rows in each table.
func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM frames),
			(SELECT COUNT(DISTINCT station) FROM observations),
			(SELECT COUNT(*) FROM issues),
			(SELECT COUNT(*) FROM forecasts),
			(SELECT COUNT(*) FROM forecasts WHERE obs_dbz IS NOT NULL),
			(SELECT COUNT(*) FROM lookups)`).
		Scan(&c.Frames, &c.Stations, &c.Issues, &c.Forecasts, &c.Verified, &c.Lookups)
	return c, err
}
