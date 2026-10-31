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
	errSuperseded = platform.NewError(http.StatusConflict, "operation_superseded")
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

// operations lists the first operations matching where, most urgent and oldest first, and
// how many match in all.
func operations(ctx context.Context, pool *pgxpool.Pool, where string, args ...any) ([]Operation, int, error) {
	rows, err := pool.Query(ctx, `
		SELECT o.id, g.id, o.type, `+holder+`, coalesce(e.full_name, g.account), t.name, l.name, o.status, o.attempts, o.last_error,
		       CASE WHEN o.status = 'FAILED' THEN o.completed_at END,
		       o.version, o.created_at, count(*) OVER ()
		FROM operations o JOIN grants g ON g.id = o.grant_id LEFT JOIN employees e ON e.id = g.employee_id
		JOIN tools t ON t.id = g.tool_id JOIN access_levels l ON l.id = g.access_level_id
		WHERE `+where+` ORDER BY o.priority, o.created_at, o.id LIMIT 200`, args...) // shortcut: first 200, with the total; each resolved one makes room
	if err != nil {
		return nil, 0, err
	}
	total := 0
	ops, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Operation, error) {
		var o Operation
		return o, row.Scan(&o.ID, &o.GrantID, &o.Type, &o.Employee.ID, &o.Employee.FullName, &o.Tool, &o.Level, &o.Status,
			&o.Attempts, &o.LastError, &o.FailedAt, &o.Version, &o.CreatedAt, &total)
	})
	return ops, total, err
}

// FailedOperations lists the operations waiting for a manual retry (O3). The caller checks
// who may read Exceptions.
func FailedOperations(ctx context.Context, pool *pgxpool.Pool) ([]Operation, int, error) {
	return operations(ctx, pool, "o.status = 'FAILED'")
}

// SuspensionGaps lists the access a suspended employee still has in a tool because its
// SUSPEND has failed at least once: from that attempt until the suspension is verified or
// confirmed, whether it is retrying, waiting for a manual retry or reworked as a task (D7).
// The caller checks who may read them.
func SuspensionGaps(ctx context.Context, pool *pgxpool.Pool) ([]Operation, int, error) {
	return operations(ctx, pool, `o.type = 'SUSPEND' AND o.status IN ('PENDING', 'IN_PROGRESS', 'FAILED') AND o.last_error IS NOT NULL
		AND e.status = 'SUSPENDED'`)
}

// Overlap is a level change whose old level is still held because its removal failed (D4):
// listed from the removal's first failed attempt until it is verified or confirmed.
type Overlap struct {
	Employee    Person    `json:"employee"`
	Tool        string    `json:"tool"`
	OldGrantID  uuid.UUID `json:"oldGrantId"`
	OldLevel    string    `json:"oldLevel"`
	NewGrantID  uuid.UUID `json:"newGrantId"`
	NewLevel    string    `json:"newLevel"`
	OperationID uuid.UUID `json:"operationId"`
	Status      string    `json:"status"`
	LastError   *string   `json:"lastError"`
}

// Overlaps lists the D4 overlaps: an ACTIVE grant replacing one whose open removal has
// failed at least once, whatever the removal's reason. The caller checks who may read them.
func Overlaps(ctx context.Context, pool *pgxpool.Pool) ([]Overlap, int, error) {
	rows, err := pool.Query(ctx, `
		SELECT e.id, e.full_name, t.name, g.id, ol.name, n.id, nl.name, o.id, o.status, o.last_error, count(*) OVER ()
		FROM grants n JOIN grants g ON g.id = n.replaces_grant_id
		JOIN operations o ON o.grant_id = g.id AND o.type = 'REVOKE' AND o.status IN ('PENDING', 'IN_PROGRESS', 'FAILED')
		JOIN employees e ON e.id = g.employee_id JOIN tools t ON t.id = g.tool_id
		JOIN access_levels ol ON ol.id = g.access_level_id JOIN access_levels nl ON nl.id = n.access_level_id
		WHERE n.status = 'ACTIVE' AND o.last_error IS NOT NULL
		ORDER BY o.created_at, o.id LIMIT 200`) // shortcut: first 200, with the total, as FailedOperations
	if err != nil {
		return nil, 0, err
	}
	total := 0
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Overlap, error) {
		var o Overlap
		return o, row.Scan(&o.Employee.ID, &o.Employee.FullName, &o.Tool, &o.OldGrantID, &o.OldLevel, &o.NewGrantID, &o.NewLevel,
			&o.OperationID, &o.Status, &o.LastError, &total)
	})
	return list, total, err
}

// Retry starts a new round of attempts for a FAILED operation with the same operation ID.
// Allowed for a Super Admin or a member of the tool's admin group, never on one's own
// grant (F2). A repeat while the operation is queued or running returns its state.
func (s *Service) Retry(ctx context.Context, actor auth.Principal, id uuid.UUID, version int) (Operation, error) {
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		var grant, employee uuid.UUID
		var method string
		var isMember bool
		err := tx.QueryRow(ctx, `SELECT g.id, `+holder+`, g.method, `+member("$2")+`
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
		if p, err := CurrentActor(ctx, tx, actor); err != nil {
			return err
		} else if !isMember && !p.HasRole("SUPER_ADMIN") { // removed while the request waited
			return platform.ErrForbidden
		}
		var employeeStatus string // an orphan account's grant has no employee
		if err := tx.QueryRow(ctx, "SELECT status FROM employees WHERE id = $1", employee).Scan(&employeeStatus); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		switch {
		case status == "PENDING" || status == "IN_PROGRESS":
			return nil
		case status != "FAILED":
			return errNotFailed
		case current != version:
			return errVersionConflict
		case typ == "SUSPEND" && employeeStatus != "SUSPENDED", typ == "RESUME" && employeeStatus != "ACTIVE":
			return errSuperseded // it would disable or restore access the wrong way (D7)
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
		to := grantStatus // a SUSPEND or RESUME never moved the grant when it failed
		switch typ {
		case "GRANT":
			to = map[bool]string{true: "PROVISIONING", false: "PENDING_FULFILLMENT"}[method == "API"]
		case "REVOKE":
			to = "REVOCATION_REQUESTED"
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
	ops, _, err := operations(ctx, s.pool, "o.id = $1", id)
	if err != nil || len(ops) == 0 {
		return Operation{}, err
	}
	return ops[0], nil
}

// StaleGrants are the open removals of grants that activated after their transfer removal
// was requested (D5): listed until verified or confirmed. The caller checks who may read them.
func StaleGrants(ctx context.Context, pool *pgxpool.Pool) ([]Operation, int, error) {
	return operations(ctx, pool, `o.type = 'REVOKE' AND o.status IN ('PENDING', 'IN_PROGRESS', 'FAILED') AND g.stale_at IS NOT NULL`)
}

// SecurityRevocations are the open removals of terminated employees (D7, O7), the only
// priority-0 removals: listed until verified or confirmed, even after a rehire. The caller
// checks who may read them.
func SecurityRevocations(ctx context.Context, pool *pgxpool.Pool) ([]Operation, int, error) {
	return operations(ctx, pool, `o.type = 'REVOKE' AND o.status IN ('PENDING', 'IN_PROGRESS', 'FAILED') AND o.priority = 0`)
}
