package requests

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/catalog"
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/reviews"
)

// decided are the step statuses that count someone as an approver of the item.
var decided = map[string]bool{"APPROVED": true, "APPROVED_BY_SUBMISSION": true, "SUPER_ADMIN_OVERRIDE": true}

// itemState is a locked item with what the rules need.
type itemState struct {
	id, beneficiary, submitter uuid.UUID
	status                     string
	version                    int
	steps                      []Step
}

func (it itemState) excluded() Excluded {
	var deciders []uuid.UUID
	for _, s := range it.steps {
		if decided[s.Status] && s.DecidedBy != nil {
			deciders = append(deciders, *s.DecidedBy)
		}
	}
	return Exclusions(it.beneficiary, it.submitter, deciders)
}

// lockItem locks the item, then reads it and its steps in new statements, so a change
// committed while waiting is seen (M3: lock first, then read).
func lockItem(ctx context.Context, tx pgx.Tx, id uuid.UUID) (itemState, error) {
	tag, err := tx.Exec(ctx, "SELECT 1 FROM request_items WHERE id = $1 FOR UPDATE", id)
	if err != nil {
		return itemState{}, err
	}
	if tag.RowsAffected() == 0 {
		return itemState{}, platform.ErrNotFound
	}
	it := itemState{id: id}
	if err := tx.QueryRow(ctx, `SELECT i.status, i.version, r.beneficiary_id, r.submitted_by
		FROM request_items i JOIN requests r ON r.id = i.request_id WHERE i.id = $1`, id).
		Scan(&it.status, &it.version, &it.beneficiary, &it.submitter); err != nil {
		return itemState{}, err
	}
	steps, err := loadSteps(ctx, tx, []uuid.UUID{id})
	it.steps = steps[id]
	return it, err
}

// lockStep locks the step's item and returns both.
func lockStep(ctx context.Context, tx pgx.Tx, stepID uuid.UUID) (itemState, Step, error) {
	var itemID uuid.UUID // a step never moves to another item
	err := tx.QueryRow(ctx, "SELECT item_id FROM approval_steps WHERE id = $1", stepID).Scan(&itemID)
	if errors.Is(err, pgx.ErrNoRows) {
		return itemState{}, Step{}, platform.ErrNotFound
	}
	if err != nil {
		return itemState{}, Step{}, err
	}
	it, err := lockItem(ctx, tx, itemID)
	if err != nil {
		return itemState{}, Step{}, err
	}
	for _, s := range it.steps {
		if s.ID == stepID {
			return it, s, nil
		}
	}
	return itemState{}, Step{}, platform.ErrNotFound
}

// checkOpen: the item is pending and the step is open at the expected version.
func (it itemState) checkOpen(step Step, version int, to string) error {
	switch {
	case it.status != "PENDING_APPROVAL":
		return errItemNotPending
	case step.Status == "WAITING":
		return errStepNotOpen
	case !canMove(stepMoves, step.Status, to):
		return errStepDecided
	case step.Version != version:
		return errVersionConflict
	}
	return nil
}

func candidate(ctx context.Context, tx pgx.Tx, id *uuid.UUID) (*Candidate, *uuid.UUID, error) {
	if id == nil {
		return nil, nil, nil
	}
	// Lock first, then read: a suspension or termination committing now is seen (D7).
	if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR SHARE", *id); err != nil {
		return nil, nil, err
	}
	var status string
	var manager *uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT status, manager_id FROM employees WHERE id = $1", *id).Scan(&status, &manager); err != nil {
		return nil, nil, err
	}
	return &Candidate{ID: *id, Active: status == "ACTIVE"}, manager, nil
}

// advance opens the item's next waiting step, routed now (F2), or approves the item when
// every step is done. The caller holds the item lock; loc is the company timezone.
func advance(ctx context.Context, tx pgx.Tx, now time.Time, loc *time.Location, actor, itemID uuid.UUID) error {
	it, err := lockItem(ctx, tx, itemID)
	if err != nil {
		return err
	}
	var next *Step
	for i, s := range it.steps {
		if s.Status == "WAITING" {
			next = &it.steps[i]
			break
		}
	}
	if next == nil {
		return finish(ctx, tx, now, loc, actor, it)
	}
	route, err := routeStep(ctx, tx, it, *next)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE approval_steps SET status = 'PENDING', assignee_id = $2, pool = NULLIF($3, ''), routing_note = $4,
		opened_at = $5, version = version + 1 WHERE id = $1 AND status = 'WAITING'`, next.ID, route.AssigneeID, route.Pool, route.Note, now)
	return err
}

// routeStep finds the step's approver now (F2): the beneficiary's manager or the tool's
// owner when available, else their fallback.
func routeStep(ctx context.Context, tx pgx.Tx, it itemState, next Step) (Route, error) {
	switch next.Type {
	case "MANAGER":
		var managerID *uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT manager_id FROM employees WHERE id = $1", it.beneficiary).Scan(&managerID); err != nil {
			return Route{}, err
		}
		manager, skipID, err := candidate(ctx, tx, managerID)
		if err != nil {
			return Route{}, err
		}
		skip, _, err := candidate(ctx, tx, skipID)
		if err != nil {
			return Route{}, err
		}
		return RouteManager(manager, skip, it.excluded()), nil
	case "TOOL_OWNER":
		var ownerID uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT t.owner_id FROM request_items i JOIN tools t ON t.id = i.tool_id WHERE i.id = $1",
			it.id).Scan(&ownerID); err != nil {
			return Route{}, err
		}
		owner, _, err := candidate(ctx, tx, &ownerID)
		if err != nil {
			return Route{}, err
		}
		return RouteToolOwner(*owner, it.excluded()), nil
	}
	return toPool("InfoSec"), nil
}

// finish runs when every step is decided. An item that can no longer apply is cancelled
// first, without reaching the tool or asking anyone else: a retired level or tool, an
// expiry date already passed (D6), or an extension or level change of access that has
// ended. Then steps the level now requires are added and routed (F2: stricter wins);
// otherwise the item is approved and applied in this transaction: a new grant, or for an
// EXTENSION the grant's new expiry date.
func finish(ctx context.Context, tx pgx.Tx, now time.Time, loc *time.Location, actor uuid.UUID, it itemState) error {
	// The beneficiary first, as Submit does (A9); CreateGrant's lock is then a re-lock.
	if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", it.beneficiary); err != nil {
		return err
	}
	var toolID uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT tool_id FROM request_items WHERE id = $1", it.id).Scan(&toolID); err != nil {
		return err
	}
	// Catalog changes lock the tool: lock first, then read the current flags and statuses.
	if _, err := tx.Exec(ctx, "SELECT 1 FROM tools WHERE id = $1 FOR SHARE", toolID); err != nil {
		return err
	}
	// The grant a change acts on moves only under the beneficiary's lock, held here.
	var toolStatus, levelStatus, kind string
	var owner, infosec, expired, ended bool
	var grant *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT t.status, l.status, l.requires_tool_owner, l.requires_infosec, r.kind, i.grant_id,
		       coalesce(i.expires_on < $2, false),
		       i.grant_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM grants g WHERE g.id = i.grant_id
		           AND g.status = 'ACTIVE' AND (g.expires_on IS NULL OR g.expires_on >= $2))
		FROM request_items i JOIN requests r ON r.id = i.request_id JOIN tools t ON t.id = i.tool_id
		JOIN access_levels l ON l.id = i.access_level_id
		WHERE i.id = $1`, it.id, access.Today(now, loc)).Scan(&toolStatus, &levelStatus, &owner, &infosec, &kind, &grant, &expired, &ended); err != nil {
		return err
	}
	reason := access.RetiredReason(toolStatus, levelStatus)
	switch {
	case expired: // access lasts through its date: a passed date gives nothing
		reason = "expired before approval"
	case ended:
		reason = "the access has ended"
	}
	if reason != "" {
		return closeItem(ctx, tx, now, &actor, it, "CANCELLED", reason)
	}
	if added := policySteps(catalog.Steps(owner, infosec), it.steps); len(added) > 0 {
		for i, typ := range added {
			var stepID uuid.UUID
			if err := tx.QueryRow(ctx, "INSERT INTO approval_steps (item_id, position, type) VALUES ($1, $2, $3) RETURNING id",
				it.id, len(it.steps)+i+1, typ).Scan(&stepID); err != nil {
				return err
			}
			if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor, Action: "STEP_ADDED_BY_POLICY", Entity: "approval_step",
				EntityID: &stepID, SubjectID: &it.beneficiary, Details: map[string]any{"itemId": it.id, "type": typ}}); err != nil {
				return err
			}
		}
		return advance(ctx, tx, now, loc, actor, it.id)
	}
	if err := closeItem(ctx, tx, now, &actor, it, "APPROVED", ""); err != nil {
		return err
	}
	if kind == "EXTENSION" {
		return access.Extend(ctx, tx, actor, *grant, it.id)
	}
	_, err := access.CreateGrant(ctx, tx, now, actor, it.id)
	return err
}

// closeItem moves the item to a final status and cancels its undecided steps. A system
// cancellation keeps its reason on the item for the requester.
func closeItem(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, it itemState, to, reason string) error {
	if !canMove(itemMoves, it.status, to) {
		return errItemNotPending
	}
	if _, err := tx.Exec(ctx, `UPDATE request_items SET status = $2, closed_at = $3, version = version + 1,
		closed_reason = CASE WHEN $2 = 'CANCELLED' THEN $4 END WHERE id = $1`, it.id, to, now, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE approval_steps SET status = 'CANCELLED', version = version + 1
		WHERE item_id = $1 AND status IN ('WAITING', 'PENDING')`, it.id); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "ITEM_" + to, Entity: "request_item", EntityID: &it.id,
		SubjectID: &it.beneficiary, Reason: reason})
}

// Approve decides the actor's open step.
func (s *Service) Approve(ctx context.Context, actor auth.Principal, stepID uuid.UUID, version int) (Item, error) {
	return s.decide(ctx, actor, stepID, version, "APPROVED", "")
}

// Reject decides the actor's open step with a reason; the item is rejected for good (O5).
func (s *Service) Reject(ctx context.Context, actor auth.Principal, stepID uuid.UUID, version int, reason string) (Item, error) {
	reason, err := checkText(reason, "reason", "reason_required")
	if err != nil {
		return Item{}, err
	}
	return s.decide(ctx, actor, stepID, version, "REJECTED", reason)
}

// decide applies one decision. The first one wins; a later or unauthorized attempt is
// rejected and recorded in its own transaction, so the record survives the rollback (D9).
func (s *Service) decide(ctx context.Context, actor auth.Principal, stepID uuid.UUID, version int, to, reason string) (Item, error) {
	var out Item
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		it, step, err := lockStep(ctx, tx, stepID)
		if err != nil {
			return err
		}
		if err := access.StillActive(ctx, tx, actor.EmployeeID); err != nil {
			return err
		}
		if step.Status == to && step.DecidedBy != nil && *step.DecidedBy == actor.EmployeeID {
			out, err = loadItem(ctx, tx, it.id) // a repeat: the current state, no new event
			return err
		}
		allowed := CanDecide(actor, step, it.excluded())
		if errors.Is(allowed, platform.ErrForbidden) {
			return allowed
		}
		if err := it.checkOpen(step, version, to); err != nil {
			return err
		}
		if allowed != nil {
			return allowed
		}
		if _, err := tx.Exec(ctx, `UPDATE approval_steps SET status = $2, decided_by = $3, decided_at = $4, reason = NULLIF($5, ''),
			version = version + 1 WHERE id = $1`, stepID, to, actor.EmployeeID, now, reason); err != nil {
			return err
		}
		action := map[string]string{"APPROVED": "STEP_APPROVED", "REJECTED": "STEP_REJECTED"}[to]
		if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: action, Entity: "approval_step", EntityID: &stepID,
			SubjectID: &it.beneficiary, Reason: reason, Details: map[string]any{"itemId": it.id, "type": step.Type, "position": step.Position}}); err != nil {
			return err
		}
		if to == "REJECTED" {
			err = closeItem(ctx, tx, now, &actor.EmployeeID, it, "REJECTED", reason)
		} else {
			err = advance(ctx, tx, now, s.loc, actor.EmployeeID, it.id)
		}
		if err != nil {
			return err
		}
		out, err = loadItem(ctx, tx, it.id)
		return err
	})
	var rejected *platform.Error
	if errors.As(err, &rejected) && rejected != platform.ErrNotFound {
		if rerr := s.recordAttempt(ctx, actor, stepID, to, rejected.Code); rerr != nil {
			return Item{}, rerr
		}
	}
	return out, err
}

func (s *Service) recordAttempt(ctx context.Context, actor auth.Principal, stepID uuid.UUID, to, code string) error {
	return platform.InTx(ctx, s.pool, func(tx pgx.Tx, _ time.Time) error {
		return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "APPROVAL_ATTEMPT_REJECTED", Entity: "approval_step",
			EntityID: &stepID, Details: map[string]any{"attempted": to, "error": code}})
	})
}

// Withdraw cancels a pending item, or an approved one before work starts on its grant;
// only its beneficiary or submitter may (O5).
func (s *Service) Withdraw(ctx context.Context, actor auth.Principal, itemID uuid.UUID, version int, reason string) (Item, error) {
	reason, err := checkText(reason, "reason", "reason_required")
	if err != nil {
		return Item{}, err
	}
	var out Item
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		it, err := lockItem(ctx, tx, itemID)
		if err != nil {
			return err
		}
		if actor.EmployeeID != it.beneficiary && actor.EmployeeID != it.submitter {
			return platform.ErrNotFound
		}
		if it.status != "WITHDRAWN" { // a repeat returns the current state
			if !canMove(itemMoves, it.status, "WITHDRAWN") {
				return errItemNotPending
			}
			if it.version != version {
				return errVersionConflict
			}
			if it.status == "APPROVED" {
				if err := access.WithdrawGrant(ctx, tx, now, actor.EmployeeID, itemID, reason); err != nil {
					return err
				}
			}
			if err := closeItem(ctx, tx, now, &actor.EmployeeID, it, "WITHDRAWN", reason); err != nil {
				return err
			}
		}
		out, err = loadItem(ctx, tx, itemID)
		return err
	})
	return out, err
}

// Reassign moves an open step to another eligible person, with a reason (D9). Only
// InfoSec and Super Admins reassign, never to themselves and never on an item they
// requested, benefit from or already decided a step of.
func (s *Service) Reassign(ctx context.Context, actor auth.Principal, stepID uuid.UUID, version int, assigneeID uuid.UUID, reason string) (Item, error) {
	if !actor.HasRole("INFOSEC") && !actor.HasRole("SUPER_ADMIN") {
		return Item{}, platform.ErrForbidden
	}
	reason, err := checkText(reason, "reason", "reason_required")
	if err != nil {
		return Item{}, err
	}
	var out Item
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		it, step, err := lockStep(ctx, tx, stepID)
		if err != nil {
			return err
		}
		if err := access.StillActive(ctx, tx, actor.EmployeeID); err != nil {
			return err
		}
		if it.excluded()[actor.EmployeeID] != "" { // nobody picks who decides an item they are part of
			return errSeparation
		}
		if err := it.checkOpen(step, version, "APPROVED"); err != nil {
			return err
		}
		if assigneeID == actor.EmployeeID { // that would be an override without D1's record and notifications
			return platform.ValidationError("assigneeId", "self_assignment")
		}
		if err := checkAssignee(ctx, tx, it, step, assigneeID); err != nil {
			return err
		}
		if err := people.CheckNotAuditor(ctx, tx, assigneeID, "assigneeId"); err != nil { // O7 note 2026-10-31
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE approval_steps SET assignee_id = $2, pool = NULL, routing_note = $3, version = version + 1
			WHERE id = $1`, stepID, assigneeID, "reassigned: "+reason); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "STEP_REASSIGNED", Entity: "approval_step",
			EntityID: &stepID, SubjectID: &it.beneficiary, Reason: reason, Details: map[string]any{"itemId": it.id, "type": step.Type,
				"fromAssigneeId": step.AssigneeID, "fromPool": step.Pool, "toAssigneeId": assigneeID}}); err != nil {
			return err
		}
		if err := notify.ToEmployee(ctx, tx, assigneeID, "APPROVAL_ASSIGNED", "approval_step", stepID,
			map[string]any{"itemId": it.id, "reason": reason}); err != nil {
			return err
		}
		out, err = loadItem(ctx, tx, it.id)
		return err
	})
	return out, err
}

// checkAssignee: the new assignee is ACTIVE, not excluded by separation of duties and,
// for an InfoSec step, holds InfoSec.
func checkAssignee(ctx context.Context, tx pgx.Tx, it itemState, step Step, id uuid.UUID) error {
	// Lock first, then read in a new statement: a role change committed while we waited is seen (M3).
	tag, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR SHARE", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return platform.ValidationError("assigneeId", "assignee_not_found")
	}
	var status string
	var infosec bool
	err = tx.QueryRow(ctx, `SELECT status, EXISTS (SELECT 1 FROM role_assignments
		WHERE employee_id = $1 AND role = 'INFOSEC' AND valid_to IS NULL) FROM employees WHERE id = $1`, id).Scan(&status, &infosec)
	switch {
	case err != nil:
		return err
	case status != "ACTIVE":
		return platform.ValidationError("assigneeId", "assignee_not_active")
	case it.excluded()[id] != "":
		return platform.ValidationError("assigneeId", "separation_of_duties")
	case step.Type == "INFOSEC" && !infosec:
		return platform.ValidationError("assigneeId", "assignee_not_infosec")
	case step.AssigneeID != nil && *step.AssigneeID == id:
		return platform.ValidationError("assigneeId", "already_assigned")
	}
	return nil
}

// Override decides an open step as the Super Admin, with a reason (D1). It never applies
// to a closed item, so it cannot reverse a rejection, and never to the admin's own item.
func (s *Service) Override(ctx context.Context, actor auth.Principal, stepID uuid.UUID, version int, reason string) (Item, error) {
	if !actor.HasRole("SUPER_ADMIN") {
		return Item{}, platform.ErrForbidden
	}
	reason, err := checkText(reason, "reason", "reason_required")
	if err != nil {
		return Item{}, err
	}
	var out Item
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		it, step, err := lockStep(ctx, tx, stepID)
		if err != nil {
			return err
		}
		if err := access.StillActive(ctx, tx, actor.EmployeeID); err != nil {
			return err
		}
		if it.excluded()[actor.EmployeeID] != "" {
			return errSeparation
		}
		if err := it.checkOpen(step, version, "SUPER_ADMIN_OVERRIDE"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE approval_steps SET status = 'SUPER_ADMIN_OVERRIDE', decided_by = $2, decided_at = $3, reason = $4,
			version = version + 1 WHERE id = $1`, stepID, actor.EmployeeID, now, reason); err != nil {
			return err
		}
		details := map[string]any{"itemId": it.id, "type": step.Type, "position": step.Position,
			"assigneeId": step.AssigneeID, "pool": step.Pool}
		if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "SUPER_ADMIN_OVERRIDE", Entity: "approval_step",
			EntityID: &stepID, SubjectID: &it.beneficiary, Reason: reason, Details: details}); err != nil {
			return err
		}
		if err := notifyOverride(ctx, tx, it, step, details); err != nil {
			return err
		}
		if err := advance(ctx, tx, now, s.loc, actor.EmployeeID, it.id); err != nil {
			return err
		}
		out, err = loadItem(ctx, tx, it.id)
		return err
	})
	return out, err
}

// notifyOverride tells InfoSec, the beneficiary's manager and the bypassed assignee (D1).
// Each person is told once, whatever roles they hold.
func notifyOverride(ctx context.Context, tx pgx.Tx, it itemState, step Step, details map[string]any) error {
	var manager *uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT manager_id FROM employees WHERE id = $1", it.beneficiary).Scan(&manager); err != nil {
		return err
	}
	var told []uuid.UUID // notified below in person
	for _, id := range []*uuid.UUID{manager, step.AssigneeID} {
		if id != nil {
			told = append(told, *id)
		}
	}
	if err := notify.ToRole(ctx, tx, "INFOSEC", "SUPER_ADMIN_OVERRIDE", "approval_step", step.ID, details, told...); err != nil {
		return err
	}
	if manager != nil {
		if err := notify.ToEmployee(ctx, tx, *manager, "SUPER_ADMIN_OVERRIDE", "approval_step", step.ID, details); err != nil {
			return err
		}
	}
	if step.AssigneeID != nil && (manager == nil || *step.AssigneeID != *manager) {
		return notify.ToEmployee(ctx, tx, *step.AssigneeID, "SUPER_ADMIN_OVERRIDE", "approval_step", step.ID, details)
	}
	return nil
}

// eligibleInfoSec is true when an employee matching the condition may decide pool step st
// of request r: an ACTIVE InfoSec member outside the item's exclusions (mirrors Exclusions).
const eligibleInfoSec = `EXISTS (SELECT 1 FROM employees e JOIN role_assignments ra ON ra.employee_id = e.id
	WHERE %s AND ra.role = 'INFOSEC' AND ra.valid_to IS NULL AND e.status = 'ACTIVE'
	  AND e.id <> r.beneficiary_id AND e.id <> r.submitted_by
	  AND NOT EXISTS (SELECT 1 FROM approval_steps d WHERE d.item_id = st.item_id AND d.decided_by = e.id))`

// Approval is an open step with its item. Unroutable marks a pool step nobody may decide;
// only a Super Admin override or a reassignment resolves it (F2).
type Approval struct {
	Step       Step `json:"step"`
	Item       Item `json:"item"`
	Unroutable bool `json:"unroutable"`
	// Party: on an unroutable step in Exceptions, why the caller may not reassign or override
	// it (beneficiary, submitter, approver), so the UI explains the way out (BUG-A-03).
	Party string `json:"party,omitempty"`
}

// partyOf is why who may not act on item's steps (separation of duties, F2/D1), or "".
func partyOf(it Item, who uuid.UUID) string {
	var deciders []uuid.UUID
	for _, s := range it.Steps {
		if decided[s.Status] && s.DecidedBy != nil {
			deciders = append(deciders, *s.DecidedBy)
		}
	}
	party := map[string]string{"the beneficiary": "beneficiary", "the submitter": "submitter", "an earlier approver": "approver"}
	return party[Exclusions(it.Beneficiary.ID, it.SubmittedBy.ID, deciders)[who]]
}

// openSteps is the open steps of items waiting for approval, followed by the caller's filter
// over st and r and a closing parenthesis.
const openSteps = `
		FROM approval_steps st JOIN request_items i ON i.id = st.item_id JOIN requests r ON r.id = i.request_id
		WHERE st.status = 'PENDING' AND i.status = 'PENDING_APPROVAL' AND (`

// approvals lists open steps matching where (over st, r), oldest first.
func (s *Service) approvals(ctx context.Context, where string, args ...any) ([]Approval, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT st.id, st.item_id, st.pool IS NOT NULL AND NOT `+fmt.Sprintf(eligibleInfoSec, "true")+openSteps+where+`)
		ORDER BY st.opened_at, st.id LIMIT 200`, args...) // shortcut: oldest 200 only; Exceptions also counts its unroutable steps
	if err != nil {
		return nil, err
	}
	type open struct {
		step, item uuid.UUID
		unroutable bool
	}
	opens, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (open, error) {
		var o open
		return o, row.Scan(&o.step, &o.item, &o.unroutable)
	})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(opens))
	for i, o := range opens {
		ids[i] = o.item
	}
	items, err := loadItems(ctx, s.pool, "i.id = ANY($1)", ids)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID]Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	out := []Approval{}
	for _, o := range opens {
		it := byID[o.item]
		for _, st := range it.Steps {
			if st.ID == o.step {
				out = append(out, Approval{Step: st, Item: it, Unroutable: o.unroutable})
			}
		}
	}
	return out, nil
}

// Inbox lists the steps the actor may decide now: assigned to them, or in the InfoSec pool
// when they are an eligible member (D9: with the waiting time from openedAt).
func (s *Service) Inbox(ctx context.Context, actor auth.Principal) ([]Approval, error) {
	return s.approvals(ctx, "st.assignee_id = $1 OR (st.pool = 'INFOSEC' AND "+fmt.Sprintf(eligibleInfoSec, "e.id = $1")+")",
		actor.EmployeeID)
}

// Exceptions are what needs a person (O3, F2, D4): FAILED operations, pool steps nobody may
// decide, level changes whose old level is still held, failed suspensions and the open
// removals of terminated employees.
type Exceptions struct {
	FailedOperations    []access.Operation `json:"failedOperations"`
	UnroutableSteps     []Approval         `json:"unroutableSteps"`
	Overlaps            []access.Overlap   `json:"overlaps"`
	SuspensionGaps      []access.Operation `json:"suspensionGaps"`
	SecurityRevocations []access.Operation `json:"securityRevocations"`
	StaleGrants         []access.Operation `json:"staleGrants"`
	ReviewFindings      []reviews.Item     `json:"reviewFindings"`      // the first page; the review page lists them all
	ReviewFindingsTotal int                `json:"reviewFindingsTotal"` // PERF-004
	OrphanAccounts      []access.Orphan    `json:"orphanAccounts"`
	Totals              ExceptionTotals    `json:"totals"` // each capped list's full size: nothing open is hidden uncounted
}

// ExceptionTotals counts every open record of each capped Exceptions list, beyond the rows listed.
type ExceptionTotals struct {
	FailedOperations    int `json:"failedOperations"`
	UnroutableSteps     int `json:"unroutableSteps"`
	Overlaps            int `json:"overlaps"`
	SuspensionGaps      int `json:"suspensionGaps"`
	SecurityRevocations int `json:"securityRevocations"`
	StaleGrants         int `json:"staleGrants"`
	OrphanAccounts      int `json:"orphanAccounts"`
}

func (s *Service) Exceptions(ctx context.Context, actor auth.Principal) (Exceptions, error) {
	if !actor.HasRole("INFOSEC") && !actor.HasRole("SUPER_ADMIN") {
		return Exceptions{}, platform.ErrForbidden
	}
	var x Exceptions
	var err error
	if x.FailedOperations, x.Totals.FailedOperations, err = access.FailedOperations(ctx, s.pool); err != nil {
		return Exceptions{}, err
	}
	unroutable := "st.pool IS NOT NULL AND NOT " + fmt.Sprintf(eligibleInfoSec, "true")
	steps, err := s.approvals(ctx, unroutable)
	if err != nil {
		return Exceptions{}, err
	}
	if err := s.pool.QueryRow(ctx, "SELECT count(*)"+openSteps+unroutable+")").Scan(&x.Totals.UnroutableSteps); err != nil {
		return Exceptions{}, err
	}
	for i := range steps {
		steps[i].Party = partyOf(steps[i].Item, actor.EmployeeID)
	}
	x.UnroutableSteps = steps
	if x.Overlaps, x.Totals.Overlaps, err = access.Overlaps(ctx, s.pool); err != nil {
		return Exceptions{}, err
	}
	if x.SuspensionGaps, x.Totals.SuspensionGaps, err = access.SuspensionGaps(ctx, s.pool); err != nil {
		return Exceptions{}, err
	}
	if x.SecurityRevocations, x.Totals.SecurityRevocations, err = access.SecurityRevocations(ctx, s.pool); err != nil {
		return Exceptions{}, err
	}
	if x.StaleGrants, x.Totals.StaleGrants, err = access.StaleGrants(ctx, s.pool); err != nil {
		return Exceptions{}, err
	}
	if x.ReviewFindings, x.ReviewFindingsTotal, err = reviews.OpenFindings(ctx, s.pool); err != nil {
		return Exceptions{}, err
	}
	if x.OrphanAccounts, x.Totals.OrphanAccounts, err = access.OrphanAccounts(ctx, s.pool); err != nil {
		return Exceptions{}, err
	}
	return x, nil
}

// Open lists every open step, unroutable ones marked, for InfoSec and Super Admins, who
// reassign and override (D1, D9).
func (s *Service) Open(ctx context.Context, actor auth.Principal) ([]Approval, error) {
	if !actor.HasRole("INFOSEC") && !actor.HasRole("SUPER_ADMIN") {
		return nil, platform.ErrForbidden
	}
	return s.approvals(ctx, "true")
}
