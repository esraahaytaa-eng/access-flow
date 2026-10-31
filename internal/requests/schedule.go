package requests

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
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// TerminationLockKey makes one sweep at a time list the due terminations.
const TerminationLockKey int64 = 0x0D7_7E21

var errNotScheduled = platform.NewError(http.StatusConflict, "termination_not_scheduled")

// ScheduleTermination schedules or moves an employee's termination to a future time (D7);
// the sweep runs it then.
func (s *Service) ScheduleTermination(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, at time.Time,
	reason string) (people.Employee, error) {
	return s.personAction(ctx, actor, id, version, reason, []string{"ACTIVE", "SUSPENDED"}, people.CheckLeaverAction, func(tx pgx.Tx, now time.Time, e people.Employee, reason string) error {
		if !at.After(now) {
			return platform.ValidationError("effectiveAt", "effective_at_not_future")
		}
		if err := people.CheckNotLastSuperAdmin(ctx, tx, e); err != nil {
			return err
		}
		if err := people.SetTermination(ctx, tx, id, &at, &actor.EmployeeID, &reason); err != nil {
			return err
		}
		action, details := "TERMINATION_SCHEDULED", map[string]any{"to": at}
		if e.Termination != nil {
			action, details["from"] = "TERMINATION_RESCHEDULED", e.Termination.At
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: action, Entity: "employee",
			EntityID: &id, SubjectID: &id, Reason: reason, Details: details})
	})
}

func (s *Service) CancelTermination(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, reason string) (people.Employee, error) {
	return s.personAction(ctx, actor, id, version, reason, []string{"ACTIVE", "SUSPENDED"}, people.CheckLeaverAction, func(tx pgx.Tx, now time.Time, e people.Employee, reason string) error {
		if e.Termination == nil {
			return errNotScheduled
		}
		if err := people.SetTermination(ctx, tx, id, nil, nil, nil); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "TERMINATION_CANCELLED", Entity: "employee",
			EntityID: &id, SubjectID: &id, Reason: reason, Details: map[string]any{"from": e.Termination.At}})
	})
}

// TerminateDue runs every scheduled termination whose time has come, each in its own
// transaction with the system as actor, and returns how many it ran. It catches up after
// downtime; a schedule cancelled or moved after the listing wins (re-checked under the locks).
func TerminateDue(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var due []uuid.UUID
	err := platform.InTx(ctx, pool, func(tx pgx.Tx, now time.Time) error {
		var mine bool
		if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", TerminationLockKey).Scan(&mine); err != nil || !mine {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM employees WHERE termination_at <= $1 AND status <> 'TERMINATED'
			ORDER BY termination_at, id`, now)
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
	var errs []error // one bad employee does not hold up the others
	for _, id := range due {
		ran := false
		err := platform.InTx(ctx, pool, func(tx pgx.Tx, now time.Time) error {
			if err := people.LockLifecycle(ctx, tx); err != nil {
				return err
			}
			if err := lockFor(ctx, tx, id); err != nil {
				return err
			}
			e, err := people.LockEmployee(ctx, tx, id)
			if err != nil || e.Status == "TERMINATED" || e.Termination == nil || e.Termination.At.After(now) {
				return err
			}
			// The last active Super Admin's termination waits for a successor (D2); HR sees it overdue.
			if err := people.CheckNotLastSuperAdmin(ctx, tx, e); err != nil {
				if errors.Is(err, people.ErrLastSuperAdmin) {
					return nil
				}
				return err
			}
			ran = true
			return terminate(ctx, tx, now, nil, e, e.Termination.Reason, map[string]any{"scheduledBy": e.Termination.ScheduledBy,
				"scheduledAt": e.Termination.At})
		})
		if ran && err == nil {
			n++
		}
		errs = append(errs, err)
	}
	return n, errors.Join(errs...)
}

func (s *Service) handleSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EffectiveAt *time.Time
		Reason      *string
		Version     *int
	}
	ids, err := platform.PathIDs(r, "id")
	if err == nil {
		err = platform.DecodeRequired(w, r, &in, func() []platform.Field {
			return []platform.Field{{Name: "effectiveAt", Present: in.EffectiveAt != nil}, {Name: "reason", Present: in.Reason != nil},
				{Name: "version", Present: in.Version != nil}}
		})
	}
	var e people.Employee
	if err == nil {
		e, err = s.ScheduleTermination(r.Context(), actor(r), ids[0], *in.Version, *in.EffectiveAt, *in.Reason)
	}
	platform.Respond(w, r, http.StatusOK, e, err)
}
