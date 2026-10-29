package requests

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var errNotATransfer = platform.ValidationError("", "not_a_transfer")

// TransferInput moves an employee to a manager and department; Keep lists the grants that
// stay, each with its justification (D5).
type TransferInput struct {
	Version              int
	ManagerID            *uuid.UUID
	Department, JobTitle *string
	Reason               string
	Keep                 []access.Keep
}

// Transfer moves an active employee to a new manager or department (D5). Every reviewed
// grant is removed unless kept with a justification. With nothing kept, the employee's
// pending requests are cancelled; otherwise, on a new manager, a pending Manager step goes
// to the new manager and a request the previous manager already approved is cancelled, for
// the employee to submit again (2026-10-29).
func (s *Service) Transfer(ctx context.Context, actor auth.Principal, id uuid.UUID, in TransferInput) (people.Employee, error) {
	department, err := people.CleanText(in.Department, "department")
	if err != nil {
		return people.Employee{}, err
	}
	jobTitle, err := people.CleanText(in.JobTitle, "jobTitle")
	if err != nil {
		return people.Employee{}, err
	}
	by := &actor.EmployeeID
	return s.personAction(ctx, actor, id, in.Version, in.Reason, []string{"ACTIVE"}, func(tx pgx.Tx, now time.Time, e people.Employee, reason string) error {
		newManager := !people.SameID(e.ManagerID, in.ManagerID)
		if !newManager && people.SameText(e.Department, department) {
			return errNotATransfer
		}
		kept, revoked, err := access.RemoveExcept(ctx, tx, now, by, id, in.Keep)
		if err != nil {
			return err
		}
		if newManager && in.ManagerID != nil {
			if err := people.CheckNewManager(ctx, tx, id, *in.ManagerID); err != nil {
				return err
			}
		}
		if err := people.SetPlacement(ctx, tx, id, in.ManagerID, department, jobTitle); err != nil {
			return err
		}
		switch {
		case len(kept) == 0 && len(revoked) > 0: // HR revoked everything (D5)
			err = cancelOwn(ctx, tx, now, by, id, "transferred")
		case newManager:
			err = moveManagerSteps(ctx, tx, now, by, id)
		}
		if err != nil {
			return err
		}
		if len(kept) > 0 && in.ManagerID != nil {
			if err := notifyRetained(ctx, tx, *in.ManagerID, e, kept); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: by, Action: "EMPLOYEE_TRANSFERRED", Entity: "employee", EntityID: &id,
			SubjectID: &id, Reason: reason, Details: map[string]any{
				"from":    map[string]any{"managerId": e.ManagerID, "department": e.Department, "jobTitle": e.JobTitle},
				"to":      map[string]any{"managerId": in.ManagerID, "department": department, "jobTitle": jobTitle},
				"kept":    kept,
				"revoked": revoked,
			}})
	})
}

// moveManagerSteps applies a new manager to the employee's pending items: a pending Manager
// step is re-routed, and an item whose Manager step is already approved is cancelled.
func moveManagerSteps(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, employee uuid.UUID) error {
	own, err := ownPending(ctx, tx, employee)
	if err != nil {
		return err
	}
	for _, id := range own {
		it, err := lockItem(ctx, tx, id)
		if err != nil {
			return err
		}
		if it.status != "PENDING_APPROVAL" { // closed with a removed grant's change (endChanges)
			continue
		}
		for _, step := range it.steps {
			if step.Type != "MANAGER" {
				continue
			}
			switch step.Status {
			case "PENDING":
				err = rerouteStep(ctx, tx, actor, it, step, "transferred")
			case "APPROVED", "APPROVED_BY_SUBMISSION":
				err = closeItem(ctx, tx, now, actor, it, "CANCELLED", "transferred")
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// notifyRetained tells the new manager which access their new report kept, and why (D5).
func notifyRetained(ctx context.Context, tx pgx.Tx, manager uuid.UUID, e people.Employee, kept []access.Keep) error {
	grants := make([]map[string]any, 0, len(kept))
	for _, k := range kept {
		var tool, level string
		if err := tx.QueryRow(ctx, `SELECT t.name, l.name FROM grants g JOIN tools t ON t.id = g.tool_id
			JOIN access_levels l ON l.id = g.access_level_id WHERE g.id = $1`, k.GrantID).Scan(&tool, &level); err != nil {
			return err
		}
		grants = append(grants, map[string]any{"grantId": k.GrantID, "tool": tool, "level": level, "justification": k.Justification})
	}
	return notify.ToEmployee(ctx, tx, manager, "GRANTS_RETAINED_ON_TRANSFER", "employee", e.ID,
		map[string]any{"employee": e.FullName, "grants": grants})
}

func (s *Service) handleTransfer(w http.ResponseWriter, r *http.Request) {
	ids, err := platform.PathIDs(r, "id")
	var raw struct{ Version, ManagerID, Department, JobTitle, Reason, Keep json.RawMessage }
	if err == nil {
		err = platform.DecodeJSON(w, r, &raw)
	}
	var in TransferInput
	if err == nil {
		err = platform.DecodeRaw([]platform.RawField{{Name: "version", Raw: raw.Version, Dst: &in.Version},
			{Name: "managerId", Raw: raw.ManagerID, Dst: &in.ManagerID}, {Name: "department", Raw: raw.Department, Dst: &in.Department},
			{Name: "jobTitle", Raw: raw.JobTitle, Dst: &in.JobTitle}, {Name: "reason", Raw: raw.Reason, Dst: &in.Reason},
			{Name: "keep", Raw: raw.Keep, Dst: &in.Keep}})
	}
	var e people.Employee
	if err == nil {
		e, err = s.Transfer(r.Context(), actor(r), ids[0], in)
	}
	platform.Respond(w, r, http.StatusOK, e, err)
}
