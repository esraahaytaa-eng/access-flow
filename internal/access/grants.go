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
// Manual and Vendor grants wait for their fulfillment task (M7). A LEVEL_CHANGE item's
// grant replaces the grant the item names (D4).
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
	if err := tx.QueryRow(ctx, `INSERT INTO grants (request_item_id, employee_id, tool_id, access_level_id, method, status, expires_on,
		       replaces_grant_id, created_at)
		SELECT i.id, i.beneficiary_id, i.tool_id, i.access_level_id, t.fulfillment_method,
		       CASE WHEN t.fulfillment_method = 'API' THEN 'PROVISIONING' ELSE 'PENDING_FULFILLMENT' END, i.expires_on, i.grant_id, $2
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
// item free to withdraw, as does an item approved before grants existed.
func WithdrawGrant(ctx context.Context, tx pgx.Tx, now time.Time, actor, itemID uuid.UUID, reason string) error {
	var grant, employee uuid.UUID
	err := tx.QueryRow(ctx, "SELECT id, employee_id FROM grants WHERE request_item_id = $1", itemID).Scan(&grant, &employee)
	if errors.Is(err, pgx.ErrNoRows) {
		var extension bool
		if err := tx.QueryRow(ctx, `SELECT r.kind = 'EXTENSION' FROM request_items i JOIN requests r ON r.id = i.request_id
			WHERE i.id = $1`, itemID).Scan(&extension); err != nil {
			return err
		}
		if extension {
			return errStarted // an approved extension is already in force
		}
		return nil // approved before grants existed (M5): nothing to cancel
	}
	if err != nil {
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

// requestRemoval removes an open grant, in the caller's transaction after the employee's
// and the grant's operations' locks. A grant never sent to the tool is cancelled with no
// call; otherwise a verified REVOKE is queued (reason by), and a queued or failed GRANT is
// cancelled so it cannot re-apply access. A grant already being removed, or closed, is left
// as it is. A nil actor is the system.
func requestRemoval(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, grant, employee uuid.UUID, by, reason string) error {
	status, err := lockGrant(ctx, tx, grant)
	if err != nil {
		return err
	}
	switch status {
	case "REVOCATION_REQUESTED", "REVOCATION_FAILED", "REVOKED", "CANCELLED":
		return nil
	}
	if err := endChanges(ctx, tx, now, actor, grant, employee); err != nil {
		return err
	}
	// A waiting SUSPEND or RESUME is pointless once the access goes. An API call in flight
	// finishes first (waitsForEarlier) and is cancelled if it fails (unwanted); a claimed
	// Manual or Vendor task is a person's to-do, so it is cancelled now: its claimer cannot
	// confirm it and the removal task is ready at once (D7).
	if _, err := cancelSuspensionOps(ctx, tx, now, actor, grant, employee,
		"(status IN ('PENDING', 'FAILED') OR status = 'IN_PROGRESS' AND (SELECT method FROM grants WHERE id = $1) <> 'API')",
		"the grant is being removed"); err != nil {
		return err
	}
	var op uuid.UUID
	var opStatus string
	var neverSent bool
	err = tx.QueryRow(ctx, "SELECT id, status, first_attempt_at IS NULL FROM operations WHERE grant_id = $1 AND type = 'GRANT'",
		grant).Scan(&op, &opStatus, &neverSent)
	if errors.Is(err, pgx.ErrNoRows) {
		opStatus = "SUCCEEDED" // an imported grant (D10): the tool held it before AccessFlow
	} else if err != nil {
		return err
	}
	if opStatus == "PENDING" && neverSent {
		return cancelGrant(ctx, tx, now, actor, grant, employee, reason)
	}
	grantCancelled := opStatus == "PENDING" || opStatus == "FAILED"
	if grantCancelled {
		if err := setOp(ctx, tx, op, opStatus, "CANCELLED", "completed_at = $2", now); err != nil {
			return err
		}
	}
	if err := setGrant(ctx, tx, grant, status, "REVOCATION_REQUESTED", "closed_reason = $3, revocation_requested_by = $4",
		reason, actor); err != nil {
		return err
	}
	priority := 1
	if by == "TERMINATION" {
		priority = 0 // A6: termination removals first
	}
	if _, err := tx.Exec(ctx, `INSERT INTO operations (grant_id, type, reason, priority, next_attempt_at, created_at)
		VALUES ($1, 'REVOKE', $2, $4, $3, $3)`, grant, by, now, priority); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "GRANT_REVOCATION_REQUESTED", Entity: "grant",
		EntityID: &grant, SubjectID: &employee, Reason: reason, Details: map[string]any{"by": by, "grantOperationCancelled": grantCancelled}})
}

// LockForRemoval takes a grant's removal locks (see lockForRemoval) for a caller that may
// then change it, such as a review decision (D8).
func LockForRemoval(ctx context.Context, tx pgx.Tx, grant uuid.UUID) error {
	_, err := lockForRemoval(ctx, tx, grant)
	return err
}

// Remove takes the removal locks (see lockForRemoval) and requests the grant's removal, in
// the caller's transaction: for review decisions (D8) and orphan accounts (D10). A grant
// already being removed, or closed, is left as it is.
func Remove(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, grant uuid.UUID, by, reason string) error {
	employee, err := lockForRemoval(ctx, tx, grant)
	if err != nil {
		return err
	}
	return requestRemoval(ctx, tx, now, actor, grant, employee, by, reason)
}

// endChanges cancels the pending extension or level change of a grant being closed, so it
// never blocks the next request for the tool (D6). The caller locked the item first.
func endChanges(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, grant, employee uuid.UUID) error {
	const reason = "the access has ended"
	var item uuid.UUID // at most one: one open item per person and tool
	err := tx.QueryRow(ctx, `UPDATE request_items SET status = 'CANCELLED', closed_at = $2, closed_reason = $3, version = version + 1
		WHERE grant_id = $1 AND status = 'PENDING_APPROVAL' RETURNING id`, grant, now, reason).Scan(&item)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE approval_steps SET status = 'CANCELLED', version = version + 1
		WHERE item_id = $1 AND status IN ('WAITING', 'PENDING')`, item); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "ITEM_CANCELLED", Entity: "request_item", EntityID: &item,
		SubjectID: &employee, Reason: reason})
}

// Extend applies an approved EXTENSION item to its grant, in the caller's transaction under
// the employee's lock: the item's expiry date, or none for Until Revoked (D6). There is no
// operation: the tool already holds the level.
func Extend(ctx context.Context, tx pgx.Tx, actor, grant, itemID uuid.UUID) error {
	var employee uuid.UUID
	var from, to *string
	if err := tx.QueryRow(ctx, "SELECT employee_id, to_char(expires_on, 'YYYY-MM-DD') FROM grants WHERE id = $1 FOR UPDATE",
		grant).Scan(&employee, &from); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `UPDATE grants SET expires_on = (SELECT expires_on FROM request_items WHERE id = $2), version = version + 1
		WHERE id = $1 RETURNING to_char(expires_on, 'YYYY-MM-DD')`, grant, itemID).Scan(&to); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: &actor, Action: "GRANT_EXTENDED", Entity: "grant", EntityID: &grant,
		SubjectID: &employee, Details: map[string]any{"requestItemId": itemID, "from": from, "to": to}})
}

// Person is an employee as shown on a grant.
type Person struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"fullName"`
}

// Grant is one person's access to one level, with its display status (Fulfillment methods).
type Grant struct {
	ID                 uuid.UUID  `json:"id"`
	Employee           Person     `json:"employee"`
	ToolID             uuid.UUID  `json:"toolId"`
	Tool               string     `json:"tool"`
	AccessLevelID      uuid.UUID  `json:"accessLevelId"`
	Level              string     `json:"level"`
	Method             string     `json:"method"`
	Status             string     `json:"status"`
	DisplayStatus      string     `json:"displayStatus"`
	Evidence           *string    `json:"evidence"`
	EffectiveFrom      *time.Time `json:"effectiveFrom"`
	EffectiveTo        *time.Time `json:"effectiveTo"`
	ExpiresOn          *string    `json:"expiresOn"`
	ClosedReason       *string    `json:"closedReason"` // why it was revoked or cancelled
	Suspension         *string    `json:"suspension"`   // a SUSPEND or RESUME not yet verified or confirmed (D7)
	RetainedOnTransfer *Retention `json:"retainedOnTransfer"`
	Imported           bool       `json:"imported"` // existed before AccessFlow; approver unknown (D10)
	Version            int        `json:"version"`
	CreatedAt          time.Time  `json:"createdAt"`
}

// Retention records why a grant was kept when its holder was transferred (D5).
type Retention struct {
	At            time.Time `json:"at"`
	Justification string    `json:"justification"`
}

// reviewable are the grants a transfer decides on: open, not being or already removed. A
// SUSPENDED grant of an active employee is one whose RESUME is still on its way.
const reviewable = "g.status IN ('PROVISIONING', 'PENDING_FULFILLMENT', 'ACTIVE', 'FAILED', 'SUSPENDED')"

func (s *Service) grants(ctx context.Context, where string, args ...any) ([]Grant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, e.id, e.full_name, t.id, t.name, l.id, l.name, g.method, g.status, g.activation_evidence,
		       g.effective_from, g.effective_to, to_char(g.expires_on, 'YYYY-MM-DD'), g.closed_reason, g.version, g.created_at,
		       o.ticket_ref IS NOT NULL,
		       (SELECT CASE x.type WHEN 'SUSPEND' THEN 'SUSPENSION' ELSE 'RESUME' END ||
		               CASE WHEN x.last_error IS NULL THEN '_REQUESTED' ELSE '_FAILED' END
		        FROM operations x WHERE x.grant_id = g.id AND x.type IN ('SUSPEND', 'RESUME') AND x.status IN ('PENDING', 'IN_PROGRESS', 'FAILED')
		        ORDER BY x.id DESC LIMIT 1),
		       g.retained_at, g.retention_justification, g.import_id IS NOT NULL
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
		var retainedAt *time.Time
		var justification *string
		err := row.Scan(&g.ID, &g.Employee.ID, &g.Employee.FullName, &g.ToolID, &g.Tool, &g.AccessLevelID, &g.Level, &g.Method,
			&g.Status, &g.Evidence, &g.EffectiveFrom, &g.EffectiveTo, &g.ExpiresOn, &g.ClosedReason, &g.Version, &g.CreatedAt, &ticket,
			&g.Suspension, &retainedAt, &justification, &g.Imported)
		if retainedAt != nil {
			g.RetainedOnTransfer = &Retention{At: *retainedAt, Justification: *justification}
		}
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

// EmployeeGrants lists the grants HR decides on when transferring an employee (D5).
func (s *Service) EmployeeGrants(ctx context.Context, actor auth.Principal, id uuid.UUID) ([]Grant, error) {
	if !actor.HasRole("SUPER_ADMIN") && !actor.HasRole("HR_MANAGER") {
		return nil, platform.ErrForbidden
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM employees WHERE id = $1)", id).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, platform.ErrNotFound
	}
	return s.grants(ctx, "g.employee_id = $1 AND "+reviewable, id)
}

// Revoke gives up a grant: by its employee (O6), their current direct manager (D5) or,
// directly, a Super Admin (D1), through requestRemoval. A repeat while the grant is being
// removed returns its current state.
func (s *Service) Revoke(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, reason string) (Grant, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Grant{}, platform.ValidationError("reason", "reason_required")
	}
	if err := platform.CheckText(reason, "reason", 1000); err != nil {
		return Grant{}, err
	}
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		employee, err := lockForRemoval(ctx, tx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrNotFound
		}
		if err != nil {
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
		return requestRemoval(ctx, tx, now, &actor.EmployeeID, id, employee, by, reason)
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
