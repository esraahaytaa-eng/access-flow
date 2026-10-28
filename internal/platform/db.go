package platform

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
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

// InTx runs fn in one transaction and commits only if fn returns nil.
// now is the database time, read once, for rules, state changes and audit events (A9).
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx, now time.Time) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var now time.Time
		if err := tx.QueryRow(ctx, "SELECT now()").Scan(&now); err != nil {
			return fmt.Errorf("read transaction time: %w", err)
		}
		return fn(tx, now)
	})
}
