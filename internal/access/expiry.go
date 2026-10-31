package access

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// ExpiryLockKey is the advisory lock of an expiry round: a second instance skips the round
// (A6). Correctness does not rest on it: each grant is re-checked under its locks.
const ExpiryLockKey int64 = 0x0D6_E8B1

// openStatuses are the grants not yet being removed.
const openStatuses = `('PROVISIONING', 'PENDING_FULFILLMENT', 'ACTIVE', 'FAILED', 'SUSPENDED')`

// ExpireDue requests the removal of every open grant whose expiry date is before today in
// the company timezone (D6: access lasts through its date), each in its own transaction,
// and returns how many it expired. A grant never sent is cancelled instead.
func ExpireDue(ctx context.Context, pool *pgxpool.Pool, loc *time.Location) (int, error) {
	var due []uuid.UUID
	err := platform.InTx(ctx, pool, func(tx pgx.Tx, now time.Time) error {
		var mine bool
		if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", ExpiryLockKey).Scan(&mine); err != nil || !mine {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT id FROM grants WHERE expires_on < $1 AND status IN "+openStatuses+" ORDER BY expires_on, id",
			Today(now, loc))
		if err != nil {
			return err
		}
		due, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error // one bad grant does not hold up the others
	for _, id := range due {
		expired, err := expire(ctx, pool, loc, id)
		if expired {
			n++
		}
		errs = append(errs, err)
	}
	return n, errors.Join(errs...)
}

// expire removes one grant if, under its locks, it is still open and due: an extension
// approved after the round listed it wins.
func expire(ctx context.Context, pool *pgxpool.Pool, loc *time.Location, id uuid.UUID) (bool, error) {
	expired := false
	err := platform.InTx(ctx, pool, func(tx pgx.Tx, now time.Time) error {
		expired = false // fn may run again (platform.InTx)
		employee, err := lockForRemoval(ctx, tx, id)
		if err != nil {
			return err
		}
		var due bool
		if err := tx.QueryRow(ctx, "SELECT coalesce(status IN "+openStatuses+" AND expires_on < $2, false) FROM grants WHERE id = $1",
			id, Today(now, loc)).Scan(&due); err != nil || !due {
			return err
		}
		expired = true
		return requestRemoval(ctx, tx, now, nil, id, employee, "EXPIRED", "expired")
	})
	return expired, err
}

// lockForRemoval takes the locks a removal needs, in order: the grant's pending extension
// or level change (an approval locks its item, then the employee), the employee, then the
// grant's operations (see Runner). It returns the employee.
func lockForRemoval(ctx context.Context, tx pgx.Tx, grant uuid.UUID) (uuid.UUID, error) {
	var employee uuid.UUID // never changes; uuid.Nil for an orphan account (D10), whose locks below match nothing
	if err := tx.QueryRow(ctx, "SELECT coalesce(employee_id, '00000000-0000-0000-0000-000000000000'::uuid) FROM grants WHERE id = $1", grant).Scan(&employee); err != nil {
		return employee, err
	}
	// shortcut: a change submitted after this lock and approved before the employee's lock
	// deadlocks with this removal; PostgreSQL aborts one and platform.InTx retries it. It needs
	// a submission and its final approval inside this one transaction.
	if _, err := tx.Exec(ctx, "SELECT 1 FROM request_items WHERE grant_id = $1 AND status = 'PENDING_APPROVAL' FOR UPDATE", grant); err != nil {
		return employee, err
	}
	if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", employee); err != nil {
		return employee, err
	}
	_, err := tx.Exec(ctx, "SELECT 1 FROM operations WHERE grant_id = $1 FOR UPDATE", grant)
	return employee, err
}
