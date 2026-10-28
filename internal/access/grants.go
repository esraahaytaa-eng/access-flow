package access

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// CreateGrant creates the grant of an item at its final approval, in the caller's
// transaction, with its GRANT operation. API grants are provisioned by the runner;
// Manual and Vendor grants wait for their fulfillment task (M7).
func CreateGrant(ctx context.Context, tx pgx.Tx, now time.Time, actor, itemID uuid.UUID) (uuid.UUID, error) {
	var employee uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT beneficiary_id FROM request_items WHERE id = $1", itemID).Scan(&employee); err != nil {
		return uuid.Nil, err
	}
	// The employee's lock orders grant changes against submissions and removals (A9).
	if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", employee); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO grants (request_item_id, employee_id, tool_id, access_level_id, method, status, expires_on, created_at)
		SELECT i.id, i.beneficiary_id, i.tool_id, i.access_level_id, t.fulfillment_method,
		       CASE WHEN t.fulfillment_method = 'API' THEN 'PROVISIONING' ELSE 'PENDING_FULFILLMENT' END, i.expires_on, $2
		FROM request_items i JOIN tools t ON t.id = i.tool_id WHERE i.id = $1 RETURNING id`, itemID, now).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO operations (grant_id, type, reason, priority, next_attempt_at, created_at)
		VALUES ($1, 'GRANT', 'APPROVAL', 2, $2, $2)`, id, now); err != nil {
		return uuid.Nil, err
	}
	return id, audit.Record(ctx, tx, audit.Event{ActorID: &actor, Action: "GRANT_CREATED", Entity: "grant", EntityID: &id,
		SubjectID: &employee, Details: map[string]any{"requestItemId": itemID}})
}

var (
	errVersionConflict = platform.NewError(http.StatusConflict, "version_conflict")
	errStarted         = platform.NewError(http.StatusConflict, "fulfillment_started")
)

// WithdrawGrant cancels the grant of an approved item that is being withdrawn, in the
// caller's transaction after the item's lock, as long as no work started on it (O5): its
// task was never claimed, or the runner never sent it. A grant already cancelled leaves the
// item free to withdraw.
func WithdrawGrant(ctx context.Context, tx pgx.Tx, now time.Time, actor, itemID uuid.UUID, reason string) error {
	var grant, employee uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT id, employee_id FROM grants WHERE request_item_id = $1", itemID).Scan(&grant, &employee); err != nil {
		return err
	}
	// Lock order: employee, operations, grant (see Runner); then read.
	if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", employee); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SELECT 1 FROM operations WHERE grant_id = $1 FOR UPDATE", grant); err != nil {
		return err
	}
	var neverSent, cancelled bool
	if err := tx.QueryRow(ctx, `SELECT o.status = 'PENDING' AND o.first_attempt_at IS NULL, g.status = 'CANCELLED'
		FROM operations o JOIN grants g ON g.id = o.grant_id WHERE o.grant_id = $1 AND o.type = 'GRANT'`,
		grant).Scan(&neverSent, &cancelled); err != nil {
		return err
	}
	switch {
	case cancelled: // retired or given up before provisioning: nothing left to cancel
		return nil
	case !neverSent:
		return errStarted
	}
	return cancelGrant(ctx, tx, now, &actor, grant, employee, reason)
}

// Person is an employee as shown on a grant.
type Person struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"fullName"`
}

// Grant is one person's access to one level, with its display status (Fulfillment methods).
type Grant struct {
	ID            uuid.UUID  `json:"id"`
	Employee      Person     `json:"employee"`
	ToolID        uuid.UUID  `json:"toolId"`
	Tool          string     `json:"tool"`
	AccessLevelID uuid.UUID  `json:"accessLevelId"`
	Level         string     `json:"level"`
	Method        string     `json:"method"`
	Status        string     `json:"status"`
	DisplayStatus string     `json:"displayStatus"`
	Evidence      *string    `json:"evidence"`
	EffectiveFrom *time.Time `json:"effectiveFrom"`
	EffectiveTo   *time.Time `json:"effectiveTo"`
	ExpiresOn     *string    `json:"expiresOn"`
	ClosedReason  *string    `json:"closedReason"` // why it was revoked or cancelled
	Version       int        `json:"version"`
	CreatedAt     time.Time  `json:"createdAt"`
}

func (s *Service) grants(ctx context.Context, where string, args ...any) ([]Grant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, e.id, e.full_name, t.id, t.name, l.id, l.name, g.method, g.status, g.activation_evidence,
		       g.effective_from, g.effective_to, to_char(g.expires_on, 'YYYY-MM-DD'), g.closed_reason, g.version, g.created_at,
		       o.ticket_ref IS NOT NULL
		FROM grants g JOIN employees e ON e.id = g.employee_id
		LEFT JOIN operations o ON o.grant_id = g.id AND o.type = 'GRANT'
		JOIN tools t ON t.id = g.tool_id JOIN access_levels l ON l.id = g.access_level_id
		WHERE `+where+` ORDER BY g.created_at DESC, g.id LIMIT 200`, args...) // shortcut: newest 200 only; page when someone holds more
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Grant, error) {
		var g Grant
		var ticket bool
		err := row.Scan(&g.ID, &g.Employee.ID, &g.Employee.FullName, &g.ToolID, &g.Tool, &g.AccessLevelID, &g.Level, &g.Method,
			&g.Status, &g.Evidence, &g.EffectiveFrom, &g.EffectiveTo, &g.ExpiresOn, &g.ClosedReason, &g.Version, &g.CreatedAt, &ticket)
		g.DisplayStatus = DisplayStatus(g.Method, g.Status, ticket)
		return g, err
	})
}

// Mine lists the actor's grants, open and past (O6).
func (s *Service) Mine(ctx context.Context, actor auth.Principal) ([]Grant, error) {
	return s.grants(ctx, "g.employee_id = $1", actor.EmployeeID)
}

// Team lists the grants of the actor's direct reports (D5).
func (s *Service) Team(ctx context.Context, actor auth.Principal) ([]Grant, error) {
	return s.grants(ctx, "e.manager_id = $1", actor.EmployeeID)
}

// Revoke gives up a grant: by its employee (O6), their current direct manager (D5) or,
// directly, a Super Admin (D1).
// A grant never sent to the tool is cancelled with no call; otherwise a verified REVOKE
// is queued, and a queued or failed GRANT is cancelled so it cannot re-apply access. A
// repeat while the grant is being removed returns its current state.
func (s *Service) Revoke(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, reason string) (Grant, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Grant{}, platform.ValidationError("reason", "reason_required")
	}
	if err := platform.CheckText(reason, "reason", 1000); err != nil {
		return Grant{}, err
	}
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		var employee uuid.UUID
		err := tx.QueryRow(ctx, "SELECT employee_id FROM grants WHERE id = $1", id).Scan(&employee)
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrNotFound
		}
		if err != nil {
			return err
		}
		// Lock order: employee, operations, grant (see Runner); then read.
		if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", employee); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "SELECT 1 FROM operations WHERE grant_id = $1 FOR UPDATE", id); err != nil {
			return err
		}
		var status string
		var current int
		var manager *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT g.status, g.version, e.manager_id FROM grants g JOIN employees e ON e.id = g.employee_id
			WHERE g.id = $1 FOR UPDATE OF g`, id).Scan(&status, &current, &manager); err != nil {
			return err
		}
		by := "VOLUNTARY"
		switch {
		case actor.EmployeeID == employee:
		case manager != nil && *manager == actor.EmployeeID:
			by = "MANAGER"
		case actor.HasRole("SUPER_ADMIN"):
			by = "DIRECT" // D1: its removal task is never worked by the Super Admin
		default:
			return platform.ErrNotFound // nobody else may see it
		}
		switch status {
		case "REVOCATION_REQUESTED", "REVOCATION_FAILED", "REVOKED", "CANCELLED":
			return nil
		}
		if current != version {
			return errVersionConflict
		}
		var op uuid.UUID
		var opStatus string
		var neverSent bool
		if err := tx.QueryRow(ctx, "SELECT id, status, first_attempt_at IS NULL FROM operations WHERE grant_id = $1 AND type = 'GRANT'",
			id).Scan(&op, &opStatus, &neverSent); err != nil {
			return err
		}
		if opStatus == "PENDING" && neverSent {
			return cancelGrant(ctx, tx, now, &actor.EmployeeID, id, employee, reason)
		}
		grantCancelled := opStatus == "PENDING" || opStatus == "FAILED"
		if grantCancelled {
			if err := setOp(ctx, tx, op, opStatus, "CANCELLED", "completed_at = $2", now); err != nil {
				return err
			}
		}
		if err := setGrant(ctx, tx, id, status, "REVOCATION_REQUESTED", "closed_reason = $3, revocation_requested_by = $4",
			reason, actor.EmployeeID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO operations (grant_id, type, reason, priority, next_attempt_at, created_at)
			VALUES ($1, 'REVOKE', $2, 1, $3, $3)`, id, by, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "GRANT_REVOCATION_REQUESTED", Entity: "grant",
			EntityID: &id, SubjectID: &employee, Reason: reason, Details: map[string]any{"by": by, "grantOperationCancelled": grantCancelled}})
	})
	if err != nil {
		return Grant{}, err
	}
	gs, err := s.grants(ctx, "g.id = $1", id)
	if err != nil || len(gs) == 0 {
		return Grant{}, err
	}
	return gs[0], nil
}
