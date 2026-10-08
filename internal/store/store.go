// Package store keeps what cmd/collect gathers for the backtest in
// PostgreSQL: the radar frames recorded and the regions collected. The
// database runs next to the collector; the public API needs none.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// schemaVersion is stored in the schema_version table.
const schemaVersion = 2

// migrationLock serializes migrations between processes (the collector and
// the backtest may start together).
const migrationLock = 7_240_604

// schema creates every table. Statements run one at a time: the extended
// protocol takes a single statement per call.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS frames (
		time  BIGINT PRIMARY KEY,
		path  TEXT   NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS regions (
		tile_x       INTEGER          NOT NULL,
		tile_y       INTEGER          NOT NULL,
		lat          DOUBLE PRECISION NOT NULL,
		lon          DOUBLE PRECISION NOT NULL,
		climate      TEXT             NOT NULL,
		first_seen   BIGINT           NOT NULL,
		last_active  BIGINT           NOT NULL,
		PRIMARY KEY (tile_x, tile_y)
	)`,
}

// migrateV1 drops what schema 1 kept for the online admin page: station
// verification, user lookups and the backtest report (now a file again).
var migrateV1 = []string{
	`DROP TABLE IF EXISTS observations, issues, forecasts, lookups, reports`,
	`ALTER TABLE frames DROP COLUMN IF EXISTS recorded_at`,
}

// Store wraps the database.
type Store struct {
	db *sql.DB
}

// Open connects to the PostgreSQL database at dsn (a postgres:// URL or
// key=value string) and creates or migrates its tables.
func Open(dsn string) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("store: no database URL (set DATABASE_URL)")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	// No named prepared statements, so a transaction-mode pooler works too.
	if !strings.Contains(dsn, "default_query_exec_mode") {
		cfg.DefaultQueryExecMode = pgx.QueryExecModeExec
	}
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var version int
	err = tx.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	case version > schemaVersion:
		return fmt.Errorf("database schema %d is newer than this build (%d)", version, schemaVersion)
	case version == schemaVersion:
		return tx.Commit()
	}
	steps := schema
	if version == 1 {
		steps = append(append([]string{}, migrateV1...), schema...)
	}
	for _, stmt := range steps {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM schema_version`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES ($1)`, schemaVersion); err != nil {
		return err
	}
	return tx.Commit()
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }
