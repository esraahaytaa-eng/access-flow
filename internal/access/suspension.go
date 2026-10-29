package access

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
)

type openGrant struct {
	id     uuid.UUID
	status string
}

// lockEmployeeGrants locks the employee's operations, then their open grants (the caller
// holds the employee), and returns those grants.
func lockEmployeeGrants(ctx context.Context, tx pgx.Tx, employee uuid.UUID) ([]openGrant, error) {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM operations o JOIN grants g ON g.id = o.grant_id WHERE g.employee_id = $1
		ORDER BY o.id FOR UPDATE OF o`, employee); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id, status FROM grants WHERE employee_id = $1 AND status NOT IN ('REVOKED', 'CANCELLED')
		ORDER BY id FOR UPDATE`, employee)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (openGrant, error) {
		var g openGrant
		return g, row.Scan(&g.id, &g.status)
	})
}

// SuspendAll brings every open grant of a suspended employee to SUSPENDED, in the
// caller's transaction under the employee's lock (D7). A grant never sent to the tool is
// cancelled; one in flight or failed continues and is suspended when it activates (see
// succeed); a grant being removed is left to its removal.
func SuspendAll(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, employee uuid.UUID) error {
	grants, err := lockEmployeeGrants(ctx, tx, employee)
	if err != nil {
		return err
	}
	for _, g := range grants {
		id := g.id
		switch g.status {
		case "PROVISIONING", "PENDING_FULFILLMENT":
			var neverSent bool
			if err := tx.QueryRow(ctx, `SELECT status = 'PENDING' AND first_attempt_at IS NULL FROM operations
				WHERE grant_id = $1 AND type = 'GRANT'`, id).Scan(&neverSent); err != nil {
				return err
			}
			if neverSent {
				err = cancelGrant(ctx, tx, now, actor, id, employee, "employee suspended")
			}
		case "ACTIVE", "SUSPENDED":
			err = settle(ctx, tx, now, actor, id, employee, "SUSPENDED", "SUSPENSION")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// settle brings a locked ACTIVE or SUSPENDED grant to target (SUSPENDED or ACTIVE): it
// cancels the grant's waiting SUSPEND or RESUME and queues the one target needs, unless
// the grant is already there or on its way (needsOp). Only the newest SUSPEND or RESUME is
// ever wanted, so an older one can never apply the wrong state (see unwanted).
func settle(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, grant, employee uuid.UUID, target, reason string) error {
	sent, err := cancelSuspensionOps(ctx, tx, now, actor, grant, employee, "status IN ('PENDING', 'FAILED')", "superseded")
	if err != nil {
		return err
	}
	var effective string
	if err := tx.QueryRow(ctx, `SELECT coalesce((SELECT CASE o.type WHEN 'SUSPEND' THEN 'SUSPENDED' ELSE 'ACTIVE' END
		FROM operations o WHERE o.grant_id = g.id AND o.type IN ('SUSPEND', 'RESUME') AND o.status = 'IN_PROGRESS'
		ORDER BY o.id DESC LIMIT 1), g.status) FROM grants g WHERE g.id = $1`, grant).Scan(&effective); err != nil {
		return err
	}
	if !needsOp(target, effective, sent) {
		return nil
	}
	typ, priority, action := "SUSPEND", 1, "GRANT_SUSPENSION_REQUESTED"
	if target == "ACTIVE" {
		typ, priority, action = "RESUME", 2, "GRANT_RESUME_REQUESTED"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO operations (grant_id, type, reason, priority, next_attempt_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $5)`, grant, typ, reason, priority, now); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: action, Entity: "grant", EntityID: &grant, SubjectID: &employee,
		Details: map[string]any{"by": reason}})
}

// cancelSuspensionOps cancels the grant's SUSPEND and RESUME operations matching which (a
// condition on their status) and audits each with reason. It reports whether any of them
// may have reached the tool.
func cancelSuspensionOps(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, grant, employee uuid.UUID,
	which, reason string) (bool, error) {
	rows, err := tx.Query(ctx, `UPDATE operations SET status = 'CANCELLED', completed_at = $2, version = version + 1
		WHERE grant_id = $1 AND type IN ('SUSPEND', 'RESUME') AND `+which+` RETURNING id, first_attempt_at IS NOT NULL`, grant, now)
	if err != nil {
		return false, err
	}
	type cancelled struct {
		id   uuid.UUID
		sent bool
	}
	ops, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (cancelled, error) {
		var c cancelled
		return c, row.Scan(&c.id, &c.sent)
	})
	if err != nil {
		return false, err
	}
	sent := false
	for _, op := range ops {
		sent = sent || op.sent
		if err := audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "OPERATION_CANCELLED", Entity: "operation", EntityID: &op.id,
			SubjectID: &employee, Details: map[string]any{"reason": reason}}); err != nil {
			return false, err
		}
	}
	return sent, nil
}

// unwanted: a SUSPEND or RESUME is no longer wanted when its grant is being removed or a
// newer SUSPEND or RESUME exists. It is then cancelled instead of retried or failed, so a
// stale one in Exceptions can never restore or disable access the wrong way (D7).
func unwanted(ctx context.Context, tx pgx.Tx, op, grant uuid.UUID, typ, grantStatus string) (bool, error) {
	if typ != "SUSPEND" && typ != "RESUME" {
		return false, nil
	}
	if grantStatus == "REVOCATION_REQUESTED" || grantStatus == "REVOCATION_FAILED" {
		return true, nil
	}
	var newer bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM operations x JOIN operations o ON o.id = $2
		WHERE x.grant_id = $1 AND x.type IN ('SUSPEND', 'RESUME') AND x.status <> 'CANCELLED' AND x.id > o.id)`,
		grant, op).Scan(&newer)
	return newer, err
}

// suspensionChange records a verified or confirmed SUSPEND or RESUME: the interval always
// follows the tool (A9), the status moves only for a grant not being removed.
func suspensionChange(ctx context.Context, tx pgx.Tx, now time.Time, grant uuid.UUID, typ, grantStatus, evidence string) (string, error) {
	if typ == "SUSPEND" {
		if _, err := tx.Exec(ctx, `INSERT INTO grant_suspensions (grant_id, effective_from, evidence)
			SELECT $1, $2, $3 WHERE NOT EXISTS (SELECT 1 FROM grant_suspensions WHERE grant_id = $1 AND effective_to IS NULL)`,
			grant, now, evidence); err != nil {
			return "", err
		}
		if grantStatus == "ACTIVE" {
			return "SUSPENDED", setGrant(ctx, tx, grant, grantStatus, "SUSPENDED", "")
		}
		return grantStatus, nil
	}
	if err := endSuspension(ctx, tx, now, grant); err != nil {
		return "", err
	}
	if grantStatus == "SUSPENDED" {
		return "ACTIVE", setGrant(ctx, tx, grant, grantStatus, "ACTIVE", "")
	}
	return grantStatus, nil
}

func endSuspension(ctx context.Context, tx pgx.Tx, now time.Time, grant uuid.UUID) error {
	_, err := tx.Exec(ctx, "UPDATE grant_suspensions SET effective_to = $2 WHERE grant_id = $1 AND effective_to IS NULL", grant, now)
	return err
}

// suspendIfSuspended queues the SUSPEND of a grant that became ACTIVE while its employee is
// suspended (D7, approved 2026-10-29), so no access is active and untracked. The caller
// holds the employee.
func suspendIfSuspended(ctx context.Context, tx pgx.Tx, now time.Time, grant, employee uuid.UUID) error {
	var status string
	if err := tx.QueryRow(ctx, "SELECT status FROM employees WHERE id = $1", employee).Scan(&status); err != nil || status != "SUSPENDED" {
		return err
	}
	return settle(ctx, tx, now, nil, grant, employee, "SUSPENDED", "SUSPENSION")
}

// ResumeAll re-evaluates every grant of a reactivated employee (D7), in the caller's
// transaction under the employee's lock. A grant that would be resumed (SUSPENDED, or with
// a SUSPEND that may have reached the tool) is removed instead when its level or tool was
// retired or its expiry date has passed; the others are settled to ACTIVE, which queues a
// RESUME where the tool may hold them suspended. loc is the company timezone.
func ResumeAll(ctx context.Context, tx pgx.Tx, now time.Time, loc *time.Location, actor *uuid.UUID, employee uuid.UUID) error {
	grants, err := lockEmployeeGrants(ctx, tx, employee)
	if err != nil {
		return err
	}
	for _, g := range grants {
		if g.status != "ACTIVE" && g.status != "SUSPENDED" {
			continue
		}
		var resumes, expired bool
		var toolStatus, levelStatus string
		// Catalog retirement locks the tool: lock first, then read (A9), as cancelIfRetired.
		if _, err := tx.Exec(ctx, "SELECT 1 FROM tools WHERE id = (SELECT tool_id FROM grants WHERE id = $1) FOR SHARE", g.id); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT g.status = 'SUSPENDED' OR EXISTS (SELECT 1 FROM operations o WHERE o.grant_id = g.id
			       AND o.type = 'SUSPEND' AND (o.status = 'IN_PROGRESS' OR o.status IN ('PENDING', 'FAILED') AND o.first_attempt_at IS NOT NULL)),
			       coalesce(g.expires_on < $2, false), t.status, l.status
			FROM grants g JOIN tools t ON t.id = g.tool_id JOIN access_levels l ON l.id = g.access_level_id WHERE g.id = $1`,
			g.id, Today(now, loc)).Scan(&resumes, &expired, &toolStatus, &levelStatus); err != nil {
			return err
		}
		switch retired := RetiredReason(toolStatus, levelStatus); {
		case resumes && retired != "":
			err = requestRemoval(ctx, tx, now, actor, g.id, employee, "RETIRED", retired)
		case resumes && expired:
			err = requestRemoval(ctx, tx, now, actor, g.id, employee, "EXPIRED", "expired")
		default:
			err = settle(ctx, tx, now, actor, g.id, employee, "ACTIVE", "REACTIVATION")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// RemoveAll requests the removal of every open grant of a terminated employee at the
// highest priority (A6, D7), in the caller's transaction under the employee's lock. A
// removal already open is raised to that priority. An API GRANT in flight finishes first
// (waitsForEarlier); a claimed Manual or Vendor GRANT task is cancelled, so the removal
// never waits on its claimer, whose work the removal task undoes. An ordinary revoke lets
// it finish (M7).
func RemoveAll(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, employee uuid.UUID) error {
	grants, err := lockEmployeeGrants(ctx, tx, employee)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `UPDATE operations o SET status = 'CANCELLED', completed_at = $2, lease_token = NULL, lease_until = NULL,
		version = o.version + 1 FROM grants g WHERE g.id = o.grant_id AND g.employee_id = $1 AND o.type = 'GRANT'
		AND o.status = 'IN_PROGRESS' AND g.method <> 'API' RETURNING o.id`, employee, now)
	if err != nil {
		return err
	}
	claimed, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, op := range claimed {
		if err := audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "OPERATION_CANCELLED", Entity: "operation", EntityID: &op,
			SubjectID: &employee, Details: map[string]any{"reason": "employee terminated"}}); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE operations o SET priority = 0, version = o.version + 1 FROM grants g
		WHERE g.id = o.grant_id AND g.employee_id = $1 AND o.type = 'REVOKE' AND o.status IN ('PENDING', 'IN_PROGRESS', 'FAILED')
		AND o.priority <> 0`, employee); err != nil {
		return err
	}
	for _, g := range grants {
		if err := requestRemoval(ctx, tx, now, actor, g.id, employee, "TERMINATION", "employee terminated"); err != nil {
			return err
		}
	}
	return nil
}
