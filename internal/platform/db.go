package platform

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx", needed by goose
	"github.com/pressly/goose/v3"
)

// Migrations holds the SQL migrations embedded in the binary (A3).
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Migrate applies the pending migrations in the migrations/ directory of fsys as the owner
// user, then closes the connection. AccessFlow passes Migrations; the Mock Tool Service its own.
func Migrate(ctx context.Context, ownerURL string, fsys fs.FS) error {
	db, err := sql.Open("pgx", ownerURL)
	if err != nil {
		return fmt.Errorf("open migration connection: %w", err)
	}
	defer db.Close()

	files, err := fs.Sub(fsys, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// NewPool opens the application's connection pool with JIT compilation off. The point-in-time
// report's correlated subqueries push its planner cost past jit_above_cost, and compiling took
// 1.6–4 s of a 0.2 s query at 12k grants (PERF-010); no AccessFlow query gains from JIT.
func NewPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.RuntimeParams["jit"] = "off"
	return pgxpool.NewWithConfig(ctx, cfg)
}

// txAttempts bounds InTx's retries of a transaction PostgreSQL aborted as a deadlock or
// serialization failure: rare lock-order races between unrelated actions (known limitations).
const txAttempts = 3

// InTx runs fn in one transaction and commits only if fn returns nil.
// now is the database time, read once, for rules, state changes and audit events (A9).
// A transaction aborted as a deadlock (40P01) or serialization failure (40001) wrote nothing,
// so fn runs again in a new one, at most txAttempts times in all. fn must therefore only touch
// the database and set any outer variables it reports through on every run.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx, now time.Time) error) error {
	for attempt := 1; ; attempt++ {
		err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			var now time.Time
			if err := tx.QueryRow(ctx, "SELECT now()").Scan(&now); err != nil {
				return fmt.Errorf("read transaction time: %w", err)
			}
			return fn(tx, now)
		})
		var pgErr *pgconn.PgError
		if attempt == txAttempts || !errors.As(err, &pgErr) || (pgErr.Code != "40P01" && pgErr.Code != "40001") {
			return err
		}
		select { // a short pause lets the transaction that won finish first
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(attempt) * 20 * time.Millisecond):
		}
	}
}
