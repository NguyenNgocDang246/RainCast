package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// open connects to a fresh schema of the database at
// RAINCAST_TEST_DATABASE_URL, skipping the test when it is unset.
func open(t *testing.T) *Store {
	t.Helper()
	return openAt(t, TestDSN(t))
}

func openAt(t *testing.T, dsn string) *Store {
	t.Helper()
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRegions(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	r := Region{TileX: 116, TileY: 77, Lat: -35.5, Lon: 146.2, Climate: "midlat"}
	for _, now := range []int64{2000, 1000, 3000} {
		if err := st.TouchRegion(ctx, r, now); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Regions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].FirstSeen != 2000 || got[0].LastActive != 3000 || got[0].Climate != "midlat" {
		t.Fatalf("regions = %+v", got)
	}

	for _, ti := range []int64{1200, 600, 1800, 1200} { // a repeat is kept once
		if err := st.RecordFrame(ctx, ti, fmt.Sprint("/p", ti)); err != nil {
			t.Fatal(err)
		}
	}
	frames, err := st.FrameList(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || frames[0].Time != 1200 || frames[1].Path != "/p1800" {
		t.Fatalf("frames = %+v", frames)
	}
	recent, err := st.RecentFrames(ctx, 2)
	if err != nil || len(recent) != 2 || recent[0].Time != 1800 {
		t.Fatalf("recent = %+v %v", recent, err)
	}
	c, err := st.Counts(ctx)
	if err != nil || c != (Counts{Frames: 3, Regions: 1}) {
		t.Fatalf("counts = %+v %v", c, err)
	}
}

// Opening an up-to-date database again leaves it as it is.
func TestReopen(t *testing.T) {
	ctx := context.Background()
	dsn := TestDSN(t)
	s := openAt(t, dsn)
	if err := s.RecordFrame(ctx, 600, "/p"); err != nil {
		t.Fatal(err)
	}
	s2 := openAt(t, dsn)
	if f, err := s2.FrameList(ctx, 0); err != nil || len(f) != 1 {
		t.Fatalf("frame lost on reopen: %v %v", f, err)
	}
}

// Schema 1 (with the online admin's tables) keeps its frames and regions
// and loses the rest.
func TestMigrateFromV1(t *testing.T) {
	dsn := TestDSN(t)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
		`INSERT INTO schema_version VALUES (1)`,
		`CREATE TABLE frames (time BIGINT PRIMARY KEY, path TEXT NOT NULL, recorded_at BIGINT NOT NULL)`,
		`INSERT INTO frames VALUES (600, '/a', 1)`,
		`CREATE TABLE regions (tile_x INTEGER NOT NULL, tile_y INTEGER NOT NULL, lat DOUBLE PRECISION NOT NULL,
			lon DOUBLE PRECISION NOT NULL, climate TEXT NOT NULL, first_seen BIGINT NOT NULL, last_active BIGINT NOT NULL,
			PRIMARY KEY (tile_x, tile_y))`,
		`INSERT INTO regions VALUES (1, 2, 3, 4, 'tropical', 5, 6)`,
		`CREATE TABLE lookups (id BIGINT)`,
		`CREATE TABLE forecasts (station TEXT)`,
		`CREATE TABLE reports (name TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	s := openAt(t, dsn)
	ctx := context.Background()
	if f, err := s.FrameList(ctx, 0); err != nil || len(f) != 1 || f[0].Path != "/a" {
		t.Fatalf("frames = %+v %v", f, err)
	}
	if r, err := s.Regions(ctx); err != nil || len(r) != 1 || r[0].Climate != "tropical" {
		t.Fatalf("regions = %+v %v", r, err)
	}
	var left int
	db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name IN ('lookups', 'forecasts', 'reports')`).Scan(&left)
	var col int
	db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'frames' AND column_name = 'recorded_at'`).Scan(&col)
	if left != 0 || col != 0 {
		t.Fatalf("%d old tables and %d old columns left", left, col)
	}
}
