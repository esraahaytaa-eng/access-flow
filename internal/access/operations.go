package access

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var (
	errNotFailed  = platform.NewError(http.StatusConflict, "operation_not_failed")
	errSeparation = platform.NewError(http.StatusForbidden, "separation_of_duties")
)

// Operation is one provisioning or removal operation, as shown in Exceptions.
type Operation struct {
	ID        uuid.UUID  `json:"id"`
	GrantID   uuid.UUID  `json:"grantId"`
	Type      string     `json:"type"`
	Employee  Person     `json:"employee"`
	Tool      string     `json:"tool"`
	Level     string     `json:"level"`
	Status    string     `json:"status"`
	Attempts  int        `json:"attempts"`
	LastError *string    `json:"lastError"`
	FailedAt  *time.Time `json:"failedAt"`
	Version   int        `json:"version"`
	CreatedAt time.Time  `json:"createdAt"`
}

func operations(ctx context.Context, pool *pgxpool.Pool, where string, args ...any) ([]Operation, error) {
	rows, err := pool.Query(ctx, `
		SELECT o.id, g.id, o.type, e.id, e.full_name, t.name, l.name, o.status, o.attempts, o.last_error,
		       CASE WHEN o.status = 'FAILED' THEN o.completed_at END,
		       o.version, o.created_at
		FROM operations o JOIN grants g ON g.id = o.grant_id JOIN employees e ON e.id = g.employee_id
		JOIN tools t ON t.id = g.tool_id JOIN access_levels l ON l.id = g.access_level_id
		WHERE `+where+` ORDER BY o.priority, o.created_at LIMIT 200`, args...) // shortcut: first 200 only; page when more fail at once
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Operation, error) {
		var o Operation
		return o, row.Scan(&o.ID, &o.GrantID, &o.Type, &o.Employee.ID, &o.Employee.FullName, &o.Tool, &o.Level, &o.Status,
			&o.Attempts, &o.LastError, &o.FailedAt, &o.Version, &o.CreatedAt)
	})
}

// FailedOperations lists the operations waiting for a manual retry (O3). The caller checks
// who may read Exceptions.
func FailedOperations(ctx context.Context, pool *pgxpool.Pool) ([]Operation, error) {
	return operations(ctx, pool, "o.status = 'FAILED'")
}

// Retry starts a new round of attempts for a FAILED operation with the same operation ID.
// Allowed for a Super Admin or a member of the tool's admin group, never on one's own
// grant (F2). A repeat while the operation is queued or running returns its state.
func (s *Service) Retry(ctx context.Context, actor auth.Principal, id uuid.UUID, version int) (Operation, error) {
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		var grant, employee uuid.UUID
		var method string
		var isMember bool
		err := tx.QueryRow(ctx, `SELECT g.id, g.employee_id, g.method, `+member("$2")+`
			FROM operations o JOIN grants g ON g.id = o.grant_id JOIN tools t ON t.id = g.tool_id WHERE o.id = $1`,
			id, actor.EmployeeID).Scan(&grant, &employee, &method, &isMember)
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !isMember && !actor.HasRole("SUPER_ADMIN") {
			return platform.ErrForbidden
		}
		if employee == actor.EmployeeID {
			return errSeparation
		}
		// Lock order: employee, operations, grant (see Runner); then read.
		if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", employee); err != nil {
			return err
		}
		var typ, status string
		var current int
		if err := tx.QueryRow(ctx, "SELECT type, status, version FROM operations WHERE id = $1 FOR UPDATE", id).
			Scan(&typ, &status, &current); err != nil {
			return err
		}
		switch {
		case status == "PENDING" || status == "IN_PROGRESS":
			return nil
		case status != "FAILED":
			return errNotFailed
		case current != version:
			return errVersionConflict
		}
		grantStatus, err := lockGrant(ctx, tx, grant)
		if err != nil {
			return err
		}
		// A task goes back to the queue unclaimed; its refused ticket stays only in the audit history.
		if err := setOp(ctx, tx, id, "FAILED", "PENDING",
			"attempts = 0, next_attempt_at = $2, completed_at = NULL, claimed_by = NULL, ticket_ref = NULL", now); err != nil {
			return err
		}
		to := "REVOCATION_REQUESTED"
		if typ == "GRANT" {
			to = map[bool]string{true: "PROVISIONING", false: "PENDING_FULFILLMENT"}[method == "API"]
		}
		if err := setGrant(ctx, tx, grant, grantStatus, to, ""); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "OPERATION_RETRIED", Entity: "operation",
			EntityID: &id, SubjectID: &employee})
	})
	if err != nil {
		return Operation{}, err
	}
	ops, err := operations(ctx, s.pool, "o.id = $1", id)
	if err != nil || len(ops) == 0 {
		return Operation{}, err
	}
	return ops[0], nil
}
