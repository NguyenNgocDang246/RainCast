// Command migrate-pg copies the collected frames and regions from the SQLite
// database of earlier versions (data/raincast.db, schema 3) into the
// collector's PostgreSQL. Stop cmd/collect first.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"raincast/internal/dotenv"
	"raincast/internal/store"
)

// sqliteVersion is the PRAGMA user_version of the last SQLite schema.
const sqliteVersion = 3

// column kinds: SQLite stores booleans as 0/1 integers.
const (
	kInt = iota
	kFloat
	kText
	kBool
	kNullFloat
	kNullBool
)

type table struct {
	name  string
	cols  []string
	kinds []int
}

// tables are what the backtest needs; SQLite's station verification and
// user lookups are left behind.
var tables = []table{
	{"frames", []string{"time", "path"}, []int{kInt, kText}},
	{"regions", []string{"tile_x", "tile_y", "lat", "lon", "climate", "first_seen", "last_active"},
		[]int{kInt, kInt, kFloat, kFloat, kText, kInt, kInt}},
}

func main() {
	if err := dotenv.Load(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "load .env:", err)
		os.Exit(1)
	}
	src := flag.String("sqlite", "data/raincast.db", "SQLite database to copy from (opened read-only)")
	dbURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL to copy into (default $DATABASE_URL)")
	force := flag.Bool("force", false, "empty the PostgreSQL tables first instead of refusing when they hold data")
	flag.Parse()
	if err := run(context.Background(), *src, *dbURL, *force); err != nil {
		fmt.Fprintln(os.Stderr, "migrate-pg:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, src, dbURL string, force bool) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	lite, err := sql.Open("sqlite", "file:"+src+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer lite.Close()
	var version int
	if err := lite.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("sqlite: %w", err)
	}
	if version != sqliteVersion {
		return fmt.Errorf("sqlite schema is version %d, want %d: open it once with the last SQLite build of raincast to migrate it", version, sqliteVersion)
	}

	st, err := store.Open(dbURL)
	if err != nil {
		return err
	}
	defer st.Close()
	conn, err := st.DB().Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		return copyAll(ctx, lite, dc.(*stdlib.Conn).Conn(), force)
	})
}

func copyAll(ctx context.Context, lite *sql.DB, pg *pgx.Conn, force bool) error {
	tx, err := pg.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var names []string
	for _, t := range tables {
		names = append(names, t.name)
	}
	var busy []string
	for _, t := range tables {
		var has bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+t.name+`)`).Scan(&has); err != nil {
			return err
		}
		if has {
			busy = append(busy, t.name)
		}
	}
	if len(busy) > 0 {
		if !force {
			return fmt.Errorf("tables %s already hold data (rerun with -force to replace it)", strings.Join(busy, ", "))
		}
		if _, err := tx.Exec(ctx, `TRUNCATE `+strings.Join(names, ", ")); err != nil {
			return err
		}
	}

	for _, t := range tables {
		rows, err := readTable(ctx, lite, t)
		if err != nil {
			return fmt.Errorf("read %s: %w", t.name, err)
		}
		n, err := tx.CopyFrom(ctx, pgx.Identifier{t.name}, t.cols, pgx.CopyFromRows(rows))
		if err != nil {
			return fmt.Errorf("copy %s: %w", t.name, err)
		}
		var pgCount int64
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM `+t.name).Scan(&pgCount); err != nil {
			return err
		}
		fmt.Printf("%-13s sqlite %6d  postgres %6d\n", t.name, len(rows), pgCount)
		if n != int64(len(rows)) || pgCount != n {
			return fmt.Errorf("%s: copied %d of %d rows", t.name, n, len(rows))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Println("done")
	return nil
}

// readTable loads every row of t, converting SQLite's 0/1 booleans.
func readTable(ctx context.Context, lite *sql.DB, t table) ([][]any, error) {
	rows, err := lite.QueryContext(ctx, `SELECT `+strings.Join(t.cols, ", ")+` FROM `+t.name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][]any{}
	for rows.Next() {
		raw := make([]any, len(t.cols))
		ptrs := make([]any, len(t.cols))
		for i, k := range t.kinds {
			switch k {
			case kInt, kBool:
				ptrs[i] = new(int64)
			case kFloat:
				ptrs[i] = new(float64)
			case kText:
				ptrs[i] = new(string)
			case kNullFloat:
				ptrs[i] = new(sql.NullFloat64)
			case kNullBool:
				ptrs[i] = new(sql.NullInt64)
			}
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, k := range t.kinds {
			switch k {
			case kInt:
				raw[i] = *ptrs[i].(*int64)
			case kBool:
				raw[i] = *ptrs[i].(*int64) != 0
			case kFloat:
				raw[i] = *ptrs[i].(*float64)
			case kText:
				raw[i] = *ptrs[i].(*string)
			case kNullFloat:
				if v := *ptrs[i].(*sql.NullFloat64); v.Valid {
					raw[i] = v.Float64
				}
			case kNullBool:
				if v := *ptrs[i].(*sql.NullInt64); v.Valid {
					raw[i] = v.Int64 != 0
				}
			}
		}
		out = append(out, raw)
	}
	return out, rows.Err()
}
