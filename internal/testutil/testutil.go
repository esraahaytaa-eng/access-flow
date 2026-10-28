// Package testutil gives integration tests real PostgreSQL databases on the
// disposable postgres-test service (DECISIONS.md A2, A10).
package testutil

import (
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Env returns a required test environment variable. Tests run only through
// `docker compose --profile test run --rm tests`, which sets them; a missing
// one fails the test instead of silently skipping it.
func Env(t testing.TB, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("%s is not set; run tests with: docker compose --profile test run --rm tests", key)
	}
	return v
}

// ConnConfig parses url and points it at database.
func ConnConfig(t testing.TB, url, database string) *pgx.ConnConfig {
	t.Helper()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse %s: %v", url, err)
	}
	cfg.Database = database
	return cfg
}

// RequireSQLState fails unless err is a PostgreSQL error with the given SQLSTATE.
func RequireSQLState(t testing.TB, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("got error %v; want PostgreSQL error %s", err, code)
	}
	if pgErr.Code != code {
		t.Fatalf("got SQLSTATE %s (%s); want %s", pgErr.Code, pgErr.Message, code)
	}
}
