package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WaitLimit bounds a test's wait for a condition (a call blocked on a lock, an operation
// claimed) and the tool calls a test holds open. A wait ends as soon as its condition holds;
// the limit is generous because parallel -race tests on a busy CI machine run slowly.
const WaitLimit = 30 * time.Second

// WhileLocked makes "a change committed while I waited for the lock" deterministic. It
// runs setup in a second transaction (which must take a row lock and make the change),
// starts call, waits until call is blocked on a lock, then commits and returns call's error.
func WhileLocked(t testing.TB, pool *pgxpool.Pool, setup func(context.Context, pgx.Tx) error, call func() error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setup(ctx, tx); err != nil {
		t.Fatalf("setup: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- call() }()
	for deadline := time.Now().Add(WaitLimit); ; time.Sleep(10 * time.Millisecond) {
		var waiting int
		// Only this test's database: other test packages run in parallel on the same server.
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("call never waited for the row lock")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return <-done
}
