package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"
)

// TestDatabaseEnv names the database tests run against.
const TestDatabaseEnv = "RAINCAST_TEST_DATABASE_URL"

// TestDSN returns the DSN of a new, empty schema in the database at
// RAINCAST_TEST_DATABASE_URL, dropped when t ends. t is skipped when the
// variable is unset.
func TestDSN(t testing.TB) string {
	t.Helper()
	base := os.Getenv(TestDatabaseEnv)
	if base == "" {
		t.Skip(TestDatabaseEnv + " is not set")
	}
	db, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	rand.Read(b)
	schema := "test_" + hex.EncodeToString(b)
	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		db.Close()
	})
	return withSearchPath(base, schema)
}

// withSearchPath adds a search_path to a URL or key=value DSN.
func withSearchPath(dsn, schema string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err == nil {
			q := u.Query()
			q.Set("search_path", schema)
			u.RawQuery = q.Encode()
			return u.String()
		}
	}
	return dsn + " search_path=" + schema
}
