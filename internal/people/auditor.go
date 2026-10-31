package people

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// An Auditor reads only the audit events and can act on nothing (O7 note 2026-10-31), so
// an Auditor account holds no operational duty: the role is refused while one is open, and
// no duty is given to an Auditor. Each refusal names one duty in Field, to resolve first.

// auditorDuties are the duties that block the AUDITOR role, checked in this order.
var auditorDuties = []struct{ field, sql string }{
	{"roles", `SELECT EXISTS (SELECT 1 FROM role_assignments WHERE employee_id = $1 AND valid_to IS NULL AND role <> 'AUDITOR')`},
	{"adminGroups", `SELECT EXISTS (SELECT 1 FROM admin_group_memberships WHERE employee_id = $1 AND valid_to IS NULL)`},
	{"toolOwnership", `SELECT EXISTS (SELECT 1 FROM tools WHERE owner_id = $1 AND status = 'ACTIVE')`},
	{"directReports", `SELECT EXISTS (SELECT 1 FROM employees WHERE manager_id = $1 AND status <> 'TERMINATED')`},
	{"pendingApprovals", `SELECT EXISTS (SELECT 1 FROM approval_steps WHERE assignee_id = $1 AND status = 'PENDING')`},
	{"pendingReviews", `SELECT EXISTS (SELECT 1 FROM review_items WHERE reviewer_id = $1 AND decision IS NULL AND closed_at IS NULL)`},
	{"superAdminNomination", `SELECT EXISTS (SELECT 1 FROM super_admin_nominations WHERE nominee_id = $1 AND status = 'PENDING')`},
}

// checkNoDuties refuses the AUDITOR role for an employee with an open duty:
// 409 auditor_has_duties, Field naming the duty to transfer or reassign first.
func checkNoDuties(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	for _, d := range auditorDuties {
		var open bool
		if err := tx.QueryRow(ctx, d.sql, id).Scan(&open); err != nil {
			return err
		}
		if open {
			return &platform.Error{Status: http.StatusConflict, Code: "auditor_has_duties", Field: d.field}
		}
	}
	return nil
}

// CheckNotAuditor refuses to give a duty to an employee who holds AUDITOR:
// 409 employee_is_auditor, Field naming the input (ownerId, managerId, …). It share-locks
// the employee first, so it waits for a concurrent AUDITOR assignment (which locks the row
// for update) and then sees it; that assignment in turn sees the duty. An unknown employee
// passes: the caller reports it.
func CheckNotAuditor(ctx context.Context, tx pgx.Tx, id uuid.UUID, field string) error {
	if err := tx.QueryRow(ctx, "SELECT id FROM employees WHERE id = $1 FOR SHARE", id).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	var auditor bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM role_assignments WHERE employee_id = $1 AND role = 'AUDITOR' AND valid_to IS NULL)`,
		id).Scan(&auditor); err != nil {
		return err
	}
	if auditor {
		return &platform.Error{Status: http.StatusConflict, Code: "employee_is_auditor", Field: field}
	}
	return nil
}
