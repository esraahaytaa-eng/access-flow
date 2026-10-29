package people

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// LifecycleLockKey is the advisory lock that serializes person-level actions (suspend,
// reactivate, terminate, schedule), so the last-Super-Admin check counts a settled set.
// shortcut: one lock for every person-level action; they are rare HR actions, so waiting
// for each other is fine at the expected load.
const LifecycleLockKey = auth.LifecycleLockKey

var (
	errHasDirectReports = platform.NewError(http.StatusConflict, "has_direct_reports")
	errOwnsTools        = platform.NewError(http.StatusConflict, "owns_tools")
	// ErrEmployeeStatus refuses a person-level action on an employee in the wrong status.
	ErrEmployeeStatus = platform.NewError(http.StatusConflict, "employee_status")
)

// LockLifecycle takes LifecycleLockKey for the caller's transaction; it comes before any
// row lock of the action.
func LockLifecycle(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", LifecycleLockKey)
	return err
}

// LockLifecycleAs is LockLifecycle for an action by actor: an actor suspended or terminated
// since their request began acts no more (D7). Every status change holds LifecycleLockKey,
// so a plain read is current; a row lock here could deadlock with an approval.
func LockLifecycleAs(ctx context.Context, tx pgx.Tx, actor auth.Principal) error {
	if err := LockLifecycle(ctx, tx); err != nil {
		return err
	}
	var status string
	if err := tx.QueryRow(ctx, "SELECT status FROM employees WHERE id = $1", actor.EmployeeID).Scan(&status); err != nil {
		return err
	}
	if status != "ACTIVE" {
		return platform.ErrUnauthorized
	}
	return nil
}

// SetStatus moves a locked employee to status and invalidates every token they hold (A4).
func SetStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error {
	_, err := tx.Exec(ctx, "UPDATE employees SET status = $2, token_version = token_version + 1, version = version + 1 WHERE id = $1",
		id, status)
	return err
}

// EndEmployment terminates a locked employee (D7): status TERMINATED, every token invalid,
// any scheduled termination cleared, and every role and admin group membership closed and
// audited (history kept).
func EndEmployment(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, id uuid.UUID) error {
	if err := SetStatus(ctx, tx, id, "TERMINATED"); err != nil {
		return err
	}
	if err := SetTermination(ctx, tx, id, nil, nil, nil); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `UPDATE role_assignments SET valid_to = GREATEST(valid_from, $2), revoked_by = $3
		WHERE employee_id = $1 AND valid_to IS NULL RETURNING role`, id, now, actor)
	if err != nil {
		return err
	}
	roles, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, role := range roles {
		if err := audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "ROLE_REMOVED", Entity: "employee",
			EntityID: &id, SubjectID: &id, Details: map[string]any{"role": role}}); err != nil {
			return err
		}
	}
	rows, err = tx.Query(ctx, `UPDATE admin_group_memberships m SET valid_to = GREATEST(m.valid_from, $2), revoked_by = $3
		FROM admin_groups g WHERE g.id = m.group_id AND m.employee_id = $1 AND m.valid_to IS NULL RETURNING g.id, g.name`, id, now, actor)
	if err != nil {
		return err
	}
	type group struct {
		id   uuid.UUID
		name string
	}
	groups, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (group, error) {
		var g group
		return g, row.Scan(&g.id, &g.name)
	})
	if err != nil {
		return err
	}
	for _, g := range groups {
		if err := audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "ADMIN_GROUP_MEMBER_REMOVED", Entity: "admin_group",
			EntityID: &g.id, SubjectID: &id, Details: map[string]any{"group": g.name}}); err != nil {
			return err
		}
	}
	return nil
}

// SetTermination schedules (at set) or clears (at nil) a locked employee's termination.
func SetTermination(ctx context.Context, tx pgx.Tx, id uuid.UUID, at *time.Time, by *uuid.UUID, reason *string) error {
	_, err := tx.Exec(ctx, `UPDATE employees SET termination_at = $2, termination_scheduled_by = $3, termination_reason = $4,
		version = version + 1 WHERE id = $1`, id, at, by, reason)
	return err
}

// RehireInput places a rehired employee.
type RehireInput struct {
	Version              int
	ManagerID            *uuid.UUID
	Department, JobTitle *string
	Reason               string
}

// Rehire makes a terminated employee active again under the same identity (A5): a new
// temporary password, and nothing restored, neither access, roles, memberships nor
// requests (D7). Leaver removals still open keep running. It returns the employee and the
// password, shown once.
func (s *Service) Rehire(ctx context.Context, actor auth.Principal, id uuid.UUID, in RehireInput) (Employee, string, error) {
	if !canManageEmployees(actor) {
		return Employee{}, "", platform.ErrForbidden
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return Employee{}, "", platform.ValidationError("reason", "reason_required")
	}
	if err := platform.CheckText(reason, "reason", 1000); err != nil {
		return Employee{}, "", err
	}
	department, err := CleanText(in.Department, "department")
	if err != nil {
		return Employee{}, "", err
	}
	jobTitle, err := CleanText(in.JobTitle, "jobTitle")
	if err != nil {
		return Employee{}, "", err
	}
	password, err := auth.NewTemporaryPassword()
	if err != nil {
		return Employee{}, "", err
	}
	hash, err := s.auth.Hash(ctx, password)
	if err != nil {
		return Employee{}, "", err
	}
	var out Employee
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		if err := LockLifecycleAs(ctx, tx, actor); err != nil {
			return err
		}
		e, err := LockEmployee(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := CheckAccountAction(actor, id, e.Roles); err != nil {
			return err
		}
		if e.Status != "TERMINATED" {
			return ErrEmployeeStatus
		}
		if e.Version != in.Version {
			return errVersionConflict
		}
		// A rehired manager never takes back former reports' approvals and team access: HR
		// transfers them first (A5, 2026-10-29). Nobody gains a terminated manager meanwhile.
		var reports bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM employees WHERE manager_id = $1 AND status <> 'TERMINATED')",
			id).Scan(&reports); err != nil {
			return err
		}
		if reports {
			return errHasDirectReports
		}
		// Nor their tools' Tool Owner approvals: a Super Admin reassigns them first. A tool
		// owner must be active (checkOwner), so none is assigned meanwhile.
		var owns bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM tools WHERE owner_id = $1)", id).Scan(&owns); err != nil {
			return err
		}
		if owns {
			return errOwnsTools
		}
		if in.ManagerID != nil {
			if err := CheckNewManager(ctx, tx, id, *in.ManagerID); err != nil {
				return err
			}
		}
		// Changes the manager under LifecycleLockKey, which serializes the cycle check.
		if _, err := tx.Exec(ctx, `UPDATE employees SET status = 'ACTIVE', manager_id = $2, department = $3, job_title = $4,
			version = version + 1 WHERE id = $1`, id, in.ManagerID, department, jobTitle); err != nil {
			return err
		}
		if err := issueTemporaryPassword(ctx, tx, now, id, hash); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "EMPLOYEE_REHIRED", Entity: "employee",
			EntityID: &id, SubjectID: &id, Reason: reason, Details: map[string]any{
				"from": map[string]any{"managerId": e.ManagerID, "department": e.Department, "jobTitle": e.JobTitle},
				"to":   map[string]any{"managerId": in.ManagerID, "department": department, "jobTitle": jobTitle}}}); err != nil {
			return err
		}
		out, err = GetEmployee(ctx, tx, id)
		return err
	})
	if err != nil {
		return Employee{}, "", err
	}
	return out, password, nil
}
