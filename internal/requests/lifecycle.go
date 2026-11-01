package requests

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Person-level actions (D7) cross the person's record (people), their pending request
// items and approval steps (here) and their grants (access), so they run here, each in
// one transaction. Lock order: LifecycleLockKey; the pending items naming the person, as
// beneficiary or as a pending step's assignee; those items' beneficiaries (key share: the
// audit events below reference them); the person; their operations; their grants; then,
// shared, the tools of resumed grants (ResumeAll), the new manager (Transfer) and the people
// steps are re-routed to. An
// approval locks its item before its audit insert takes the approver's key lock, so the
// items come before the person.
// shortcut: these can still deadlock with the action (PostgreSQL aborts one, platform.InTx retries it):
// an approval by the person on another item of a beneficiary whose item names the person; the
// person, or someone a step is re-routed to, acting on another employee's grant or record; a
// tool change making the person its owner during reactivation.

// lockFor takes the item and beneficiary locks of an action on employee.
func lockFor(ctx context.Context, tx pgx.Tx, employee uuid.UUID) error {
	// shortcut: an item submitted after this lock and decided before the person's lock can
	// deadlock with the action; PostgreSQL aborts one and platform.InTx retries it.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM request_items i WHERE i.status = 'PENDING_APPROVAL' AND (i.beneficiary_id = $1
		OR EXISTS (SELECT 1 FROM approval_steps s WHERE s.item_id = i.id AND s.status = 'PENDING' AND s.assignee_id = $1))
		ORDER BY i.id FOR UPDATE`, employee); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT 1 FROM employees WHERE id IN (SELECT i.beneficiary_id FROM request_items i JOIN approval_steps s
		ON s.item_id = i.id WHERE i.status = 'PENDING_APPROVAL' AND s.status = 'PENDING' AND s.assignee_id = $1)
		ORDER BY id FOR KEY SHARE`, employee)
	return err
}

// endFor cancels the person's pending items and re-routes the pending steps assigned to
// them (F2, D9), after their status changed so routing sees them as unavailable.
func endFor(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, employee uuid.UUID, reason string) error {
	if err := cancelOwn(ctx, tx, now, actor, employee, reason); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT s.id, s.item_id FROM approval_steps s JOIN request_items i ON i.id = s.item_id
		WHERE s.status = 'PENDING' AND s.assignee_id = $1 AND i.status = 'PENDING_APPROVAL' ORDER BY s.id`, employee)
	if err != nil {
		return err
	}
	type stepRef struct{ step, item uuid.UUID }
	theirs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (stepRef, error) {
		var ref stepRef
		return ref, row.Scan(&ref.step, &ref.item)
	})
	if err != nil {
		return err
	}
	for _, ref := range theirs {
		it, err := lockItem(ctx, tx, ref.item)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(it.steps, func(s Step) bool { return s.ID == ref.step })
		// Routed to the person after lockFor, the item may have been decided or withdrawn
		// before its lock here: such a step is no longer theirs to re-route.
		if s := it.steps[i]; it.status != "PENDING_APPROVAL" || s.Status != "PENDING" || s.AssigneeID == nil || *s.AssigneeID != employee {
			continue
		}
		if err := rerouteStep(ctx, tx, actor, it, it.steps[i], reason); err != nil {
			return err
		}
	}
	return nil
}

// ownPending lists the person's pending items.
func ownPending(ctx context.Context, tx pgx.Tx, employee uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, "SELECT id FROM request_items WHERE beneficiary_id = $1 AND status = 'PENDING_APPROVAL' ORDER BY id", employee)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// cancelOwn cancels the person's pending items.
func cancelOwn(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, employee uuid.UUID, reason string) error {
	own, err := ownPending(ctx, tx, employee)
	if err != nil {
		return err
	}
	for _, id := range own {
		it, err := lockItem(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := closeItem(ctx, tx, now, actor, it, "CANCELLED", reason); err != nil {
			return err
		}
	}
	return nil
}

// ToolOwnerChanged routes the tool's pending Tool Owner steps again, to its new owner or their
// fallback, in the owner change's transaction (catalog.Service.OwnerChanged): only the current
// owner decides, as for review items (D8 note 2026-10-31).
func ToolOwnerChanged(ctx context.Context, tx pgx.Tx, actor *uuid.UUID, tool uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT s.id, s.item_id FROM approval_steps s JOIN request_items i ON i.id = s.item_id
		WHERE i.tool_id = $1 AND i.status = 'PENDING_APPROVAL' AND s.type = 'TOOL_OWNER' AND s.status = 'PENDING'
		  AND s.assignee_id IS DISTINCT FROM (SELECT owner_id FROM tools WHERE id = $1) ORDER BY s.item_id`, tool)
	if err != nil {
		return err
	}
	type stepRef struct{ step, item uuid.UUID }
	pending, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (stepRef, error) {
		var ref stepRef
		return ref, row.Scan(&ref.step, &ref.item)
	})
	if err != nil {
		return err
	}
	for _, ref := range pending {
		it, err := lockItem(ctx, tx, ref.item)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(it.steps, func(s Step) bool { return s.ID == ref.step })
		if it.status != "PENDING_APPROVAL" || it.steps[i].Status != "PENDING" { // decided before its lock
			continue
		}
		if err := rerouteStep(ctx, tx, actor, it, it.steps[i], "tool owner changed"); err != nil {
			return err
		}
	}
	return nil
}

// rerouteStep routes a pending step of a locked item afresh and records the move.
func rerouteStep(ctx context.Context, tx pgx.Tx, actor *uuid.UUID, it itemState, step Step, reason string) error {
	route, err := routeStep(ctx, tx, it, step)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE approval_steps SET assignee_id = $2, pool = NULLIF($3, ''), routing_note = $4,
		version = version + 1 WHERE id = $1`, step.ID, route.AssigneeID, route.Pool, route.Note); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "STEP_REROUTED", Entity: "approval_step", EntityID: &step.ID,
		SubjectID: &it.beneficiary, Reason: reason, Details: map[string]any{"itemId": it.id, "from": step.AssigneeID,
			"to": route.AssigneeID, "pool": route.Pool, "note": route.Note}})
}

// personAction runs one person-level action on id under the lock order above, after the
// permission (F4, D7), status and version checks.
// check is people.CheckAccountAction, or people.CheckLeaverAction for the actions HR takes on a
// leaving Super Admin.
func (s *Service) personAction(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, reason string, from []string,
	check func(auth.Principal, uuid.UUID, []string) error, do func(tx pgx.Tx, now time.Time, e people.Employee, reason string) error) (people.Employee, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return people.Employee{}, platform.ValidationError("reason", "reason_required")
	}
	if err := platform.CheckText(reason, "reason", 1000); err != nil {
		return people.Employee{}, err
	}
	var out people.Employee
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		if err := people.LockLifecycleAs(ctx, tx, actor); err != nil {
			return err
		}
		if err := lockFor(ctx, tx, id); err != nil {
			return err
		}
		e, err := people.LockEmployee(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := check(actor, id, e.Roles); err != nil {
			return err
		}
		if !slices.Contains(from, e.Status) {
			return people.ErrEmployeeStatus
		}
		if e.Version != version {
			return errVersionConflict
		}
		if err := do(tx, now, e, reason); err != nil {
			return err
		}
		out, err = people.GetEmployee(ctx, tx, id)
		return err
	})
	return out, err
}

// Suspend blocks an active employee (D7): login and sessions end, pending requests are
// cancelled, their approval steps re-routed and their access suspended in each tool.
func (s *Service) Suspend(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, reason string) (people.Employee, error) {
	return s.personAction(ctx, actor, id, version, reason, []string{"ACTIVE"}, people.CheckLeaverAction, func(tx pgx.Tx, now time.Time, e people.Employee, reason string) error {
		if err := people.SetStatus(ctx, tx, id, "SUSPENDED"); err != nil {
			return err
		}
		if err := people.AfterLeaving(ctx, tx, now, &actor.EmployeeID, e); err != nil {
			return err
		}
		if err := people.RecordRecovery(ctx, tx, &actor.EmployeeID, e); err != nil {
			return err
		}
		if err := endFor(ctx, tx, now, &actor.EmployeeID, id, "employee suspended"); err != nil {
			return err
		}
		if err := access.SuspendAll(ctx, tx, now, &actor.EmployeeID, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "EMPLOYEE_SUSPENDED", Entity: "employee",
			EntityID: &id, SubjectID: &id, Reason: reason})
	})
}

// Reactivate lets a suspended employee sign in again (A4: earlier tokens stay invalid) and
// re-evaluates their grants (D7): expired, revoked or retired access is never restored; the
// rest is resumed, active once verified or confirmed. Cancelled requests stay cancelled.
func (s *Service) Reactivate(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, reason string) (people.Employee, error) {
	return s.personAction(ctx, actor, id, version, reason, []string{"SUSPENDED"}, people.CheckAccountAction, func(tx pgx.Tx, now time.Time, e people.Employee, reason string) error {
		if err := people.SetStatus(ctx, tx, id, "ACTIVE"); err != nil {
			return err
		}
		if err := access.ResumeAll(ctx, tx, now, s.loc, &actor.EmployeeID, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "EMPLOYEE_REACTIVATED", Entity: "employee",
			EntityID: &id, SubjectID: &id, Reason: reason})
	})
}

// Terminate ends an active or suspended employee now (D7): login and sessions end, roles and
// admin group memberships close, pending requests are cancelled, their approval steps
// re-routed and every open grant removed at the highest priority.
func (s *Service) Terminate(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, reason string) (people.Employee, error) {
	return s.personAction(ctx, actor, id, version, reason, []string{"ACTIVE", "SUSPENDED"}, people.CheckLeaverAction, func(tx pgx.Tx, now time.Time, e people.Employee, reason string) error {
		return terminate(ctx, tx, now, &actor.EmployeeID, e, reason, nil)
	})
}

// terminate is shared by Terminate and the scheduled termination sweep (actor nil). The last
// active Super Admin is never terminated (people.ErrLastSuperAdmin).
func terminate(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, e people.Employee, reason string, details map[string]any) error {
	if err := people.CheckNotLastSuperAdmin(ctx, tx, e); err != nil {
		return err
	}
	if err := people.EndEmployment(ctx, tx, now, actor, e.ID); err != nil {
		return err
	}
	if err := people.AfterLeaving(ctx, tx, now, actor, e); err != nil {
		return err
	}
	if err := endFor(ctx, tx, now, actor, e.ID, "employee terminated"); err != nil {
		return err
	}
	if err := access.RemoveAll(ctx, tx, now, actor, e.ID); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "EMPLOYEE_TERMINATED", Entity: "employee",
		EntityID: &e.ID, SubjectID: &e.ID, Reason: reason, Details: details})
}

func (s *Service) lifecycleRoute(do func(context.Context, auth.Principal, uuid.UUID, int, string) (people.Employee, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, in, err := decodeAction(w, r, "reason")
		var e people.Employee
		if err == nil {
			e, err = do(r.Context(), actor(r), id, *in.Version, *in.Reason)
		}
		platform.Respond(w, r, http.StatusOK, e, err)
	}
}
