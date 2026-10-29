package people

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// LifecycleLockKey is the advisory lock that serializes person-level actions (suspend,
// reactivate, terminate, schedule), so the last-Super-Admin check counts a settled set.
// shortcut: one lock for every person-level action; they are rare HR actions, so waiting
// for each other is fine at the expected load.
const LifecycleLockKey int64 = 0x0D7_11FE

var errLastSuperAdmin = platform.NewError(http.StatusConflict, "last_super_admin")

// LockLifecycle takes LifecycleLockKey for the caller's transaction; it comes before any
// row lock of the action.
func LockLifecycle(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", LifecycleLockKey)
	return err
}

// KeepsASuperAdmin refuses to suspend or terminate the last active Super Admin, or to
// schedule their termination: there is no recovery path before M14 (D2, approved
// 2026-10-29). A Super Admin whose termination is scheduled does not count. The caller
// holds LifecycleLockKey.
func KeepsASuperAdmin(ctx context.Context, tx pgx.Tx, e Employee) error {
	if !slices.Contains(e.Roles, "SUPER_ADMIN") {
		return nil
	}
	var other bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM role_assignments r JOIN employees x ON x.id = r.employee_id
		WHERE r.role = 'SUPER_ADMIN' AND r.valid_to IS NULL AND x.id <> $1 AND x.status = 'ACTIVE' AND x.termination_at IS NULL)`,
		e.ID).Scan(&other); err != nil {
		return err
	}
	if !other {
		return errLastSuperAdmin
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
