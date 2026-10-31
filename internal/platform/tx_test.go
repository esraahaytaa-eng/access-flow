package platform_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestInTxCommitsAndPassesTheTransactionTime(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	testutil.CreateProbeTable(t, db)

	var txNow, dbNow time.Time
	err := platform.InTx(ctx, db.App, func(tx pgx.Tx, now time.Time) error {
		txNow = now
		if err := tx.QueryRow(ctx, "SELECT now()").Scan(&dbNow); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "INSERT INTO probe (id) VALUES (1)")
		return err
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if txNow.IsZero() || !txNow.Equal(dbNow) {
		t.Fatalf("now = %v; want the transaction's database time %v", txNow, dbNow)
	}
	if n := testutil.Count(t, db.App, "probe"); n != 1 {
		t.Fatalf("probe rows = %d; want 1 (committed)", n)
	}
}

// A transaction aborted as a deadlock or serialization failure is retried in a new one, at
// most three times in all, and only its last attempt's writes remain; other errors are not retried.
func TestInTxRetriesOnlyDeadlocksAndOnlyThreeTimes(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	testutil.CreateProbeTable(t, db)
	run := func(fails int, code string) (int, error) {
		calls := 0
		err := platform.InTx(ctx, db.App, func(tx pgx.Tx, now time.Time) error {
			calls++
			if _, err := tx.Exec(ctx, "INSERT INTO probe (id) VALUES ($1)", calls); err != nil {
				return err
			}
			if calls <= fails {
				return &pgconn.PgError{Code: code}
			}
			return nil
		})
		return calls, err
	}
	if calls, err := run(2, "40P01"); err != nil || calls != 3 {
		t.Fatalf("two deadlocks: calls %d, err %v; want 3 and success", calls, err)
	}
	if n := testutil.Count(t, db.App, "probe"); n != 1 {
		t.Fatalf("probe rows = %d; want 1 (the aborted attempts wrote nothing)", n)
	}
	if calls, err := run(5, "40001"); err == nil || calls != 3 {
		t.Fatalf("persistent serialization failures: calls %d, err %v; want 3 and the error", calls, err)
	}
	if calls, err := run(5, "23505"); err == nil || calls != 1 {
		t.Fatalf("unique violation: calls %d, err %v; want 1 and the error", calls, err)
	}
}
