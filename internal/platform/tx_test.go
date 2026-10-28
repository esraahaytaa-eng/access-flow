package platform_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

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
