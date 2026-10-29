package access

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Runner provisions and removes API access through the Mock Tool Service (A6). The
// operations table is the queue. Each step is a short transaction: claim, then record;
// the tool call runs outside any transaction. Lock order everywhere: employee, then
// operations, then grant.
type Runner struct {
	pool   *pgxpool.Pool
	client ToolClient
}

func NewRunner(pool *pgxpool.Pool, client ToolClient) *Runner {
	return &Runner{pool: pool, client: client}
}

// lease is how long a claim is exclusive; it covers one call and one lookup (5 s each).
const lease = 30 * time.Second

// claimed is an operation leased to this worker.
type claimed struct {
	op, grant, employee, tool, level, token uuid.UUID
	typ, account                            string
	attempts                                int // including this one
}

// Run handles operations until ctx ends, pausing a second when idle or after an error.
func (r *Runner) Run(ctx context.Context) {
	for ctx.Err() == nil {
		found, err := r.RunOnce(ctx)
		if err != nil {
			slog.Error("operation runner", "error", err)
		}
		if err != nil || !found {
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}

// RunOnce handles at most one due operation and reports whether there was one.
func (r *Runner) RunOnce(ctx context.Context) (bool, error) {
	c, found, err := r.claim(ctx)
	if err != nil || c == nil {
		return found, err
	}
	return true, r.record(ctx, *c, r.call(ctx, *c))
}

// claim leases the next due API operation: removals before grants (priority), oldest
// first. A removal waits while the grant's own GRANT may still reach the tool. A GRANT
// never sent whose level or tool has been retired is cancelled instead (nil, true).
func (r *Runner) claim(ctx context.Context) (*claimed, bool, error) {
	var c *claimed
	found := false
	err := platform.InTx(ctx, r.pool, func(tx pgx.Tx, now time.Time) error {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT o.id FROM operations o JOIN grants g ON g.id = o.grant_id
			WHERE g.method = 'API'
			  AND ((o.status = 'PENDING' AND o.next_attempt_at <= $1) OR (o.status = 'IN_PROGRESS' AND o.lease_until <= $1))
			  AND NOT `+waitsForEarlier+`
			ORDER BY o.priority, o.created_at LIMIT 1 FOR UPDATE OF o SKIP LOCKED`, now).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		next := claimed{op: id, token: uuid.New()}
		var neverSent bool
		// The account is the employee's email, or the identifier an import found (D10).
		if err := tx.QueryRow(ctx, `SELECT o.type, o.attempts + 1, o.first_attempt_at IS NULL, g.id, `+holder+`, coalesce(g.account, e.email),
			       g.tool_id, g.access_level_id
			FROM operations o JOIN grants g ON g.id = o.grant_id LEFT JOIN employees e ON e.id = g.employee_id WHERE o.id = $1`, id).
			Scan(&next.typ, &next.attempts, &neverSent, &next.grant, &next.employee, &next.account, &next.tool, &next.level); err != nil {
			return err
		}
		if next.typ == "GRANT" && neverSent {
			cancelled, err := cancelIfRetired(ctx, tx, now, nil, next.grant, next.employee)
			if errors.Is(err, errEmployeeBusy) {
				return nil // left PENDING for the next claim
			}
			if err != nil || cancelled {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE operations SET status = 'IN_PROGRESS', attempts = attempts + 1, lease_token = $2,
			lease_until = $3, first_attempt_at = coalesce(first_attempt_at, $4), version = version + 1 WHERE id = $1`,
			id, next.token, now.Add(lease), now); err != nil {
			return err
		}
		c = &next
		return nil
	})
	return c, found, err
}

// call applies the operation, then checks the tool's actual state: success is only a
// confirmed result whose lookup shows the expected state (A6).
func (r *Runner) call(ctx context.Context, c claimed) error {
	op := ToolOperation{ID: c.op, Type: c.typ, Tool: c.tool.String(), Account: c.account, Level: c.level.String()}
	if _, err := r.client.Apply(ctx, op); err != nil {
		return err
	}
	state, err := r.client.Account(ctx, op.Tool, op.Account)
	if err != nil {
		return err
	}
	held := slices.IndexFunc(state.Levels, func(l ToolLevel) bool { return l.Level == op.Level })
	switch {
	case c.typ == "GRANT" && (held < 0 || state.Levels[held].Status != "ACTIVE"):
		return errors.New("status lookup: the level is not active")
	case c.typ == "REVOKE" && held >= 0:
		return errors.New("status lookup: the level is still held")
	case c.typ == "SUSPEND" && (held < 0 || state.Levels[held].Status != "SUSPENDED"):
		return errors.New("status lookup: the level is not suspended")
	case c.typ == "RESUME" && (held < 0 || state.Levels[held].Status != "ACTIVE"):
		return errors.New("status lookup: the level is not active")
	}
	return nil
}

// record writes the outcome, unless the lease was lost: then the worker that re-claimed
// the operation owns the result. The employee is locked first, as by every other grant
// change: the audit event's key lock on the employee would otherwise come after the
// operation's lock and deadlock with a revoke. (The claim takes the employee only to
// cancel a retired grant, and then without waiting: see cancelIfRetired.)
func (r *Runner) record(ctx context.Context, c claimed, callErr error) error {
	return platform.InTx(ctx, r.pool, func(tx pgx.Tx, now time.Time) error {
		if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", c.employee); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, "SELECT 1 FROM operations WHERE id = $1 AND lease_token = $2 AND status = 'IN_PROGRESS' FOR UPDATE",
			c.op, c.token)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		grantStatus, err := lockGrant(ctx, tx, c.grant)
		if err != nil {
			return err
		}
		if callErr == nil {
			return succeed(ctx, tx, now, nil, c.op, c.grant, c.employee, c.typ, grantStatus, "VERIFIED", map[string]any{})
		}

		msg := strings.ToValidUTF8(callErr.Error()[:min(len(callErr.Error()), 500)], "")
		if err := audit.Record(ctx, tx, audit.Event{Action: "OPERATION_ATTEMPT_FAILED", Entity: "operation", EntityID: &c.op,
			SubjectID: &c.employee, Details: map[string]any{"attempt": c.attempts, "error": msg}}); err != nil {
			return err
		}
		wait, final := Backoff(c.attempts)
		var rejected *StatusError
		if errors.As(callErr, &rejected) && rejected.Status == http.StatusConflict {
			final = true // the tool refused the operation: retrying cannot help (O3)
		}
		stale, err := unwanted(ctx, tx, c.op, c.grant, c.typ, grantStatus)
		if err != nil {
			return err
		}
		if !final && !stale && !(c.typ == "GRANT" && grantStatus == "REVOCATION_REQUESTED") {
			return setOp(ctx, tx, c.op, "IN_PROGRESS", "PENDING", "next_attempt_at = $2, last_error = $3", now.Add(wait), msg)
		}
		return fail(ctx, tx, now, nil, c.op, c.grant, c.employee, c.typ, grantStatus, msg, map[string]any{"attempts": c.attempts})
	})
}

// holder is a grant's employee, or uuid.Nil for an orphan account (D10).
const holder = "coalesce(g.employee_id, '00000000-0000-0000-0000-000000000000'::uuid)"

// lockGrant locks a grant and returns its status.
func lockGrant(ctx context.Context, tx pgx.Tx, id uuid.UUID) (string, error) {
	var status string
	err := tx.QueryRow(ctx, "SELECT status FROM grants WHERE id = $1 FOR UPDATE", id).Scan(&status)
	return status, err
}

// succeed records a confirmed result of an IN_PROGRESS operation: VERIFIED by the runner,
// SELF_REPORTED by a task's claimer. A GRANT that completes after its removal was requested
// records when access began and leaves the removal in charge (D5). A level change's new
// grant becoming ACTIVE requests the old grant's removal in this transaction: never before
// the new level is verified or confirmed (D4, A9).
func succeed(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, op, grant, employee uuid.UUID,
	typ, grantStatus, evidence string, details map[string]any) error {
	if err := setOp(ctx, tx, op, "IN_PROGRESS", "SUCCEEDED", "completed_at = $2, last_error = NULL", now); err != nil {
		return err
	}
	details["operationId"], details["evidence"] = op, evidence
	event := func(action string) error {
		return audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: action, Entity: "grant", EntityID: &grant, SubjectID: &employee,
			Details: details})
	}
	if typ == "SUSPEND" || typ == "RESUME" {
		to, err := suspensionChange(ctx, tx, now, grant, typ, grantStatus, evidence)
		if err != nil {
			return err
		}
		if err := event(map[string]string{"SUSPEND": "GRANT_SUSPENDED", "RESUME": "GRANT_RESUMED"}[typ]); err != nil || to != "ACTIVE" {
			return err
		}
		return suspendIfSuspended(ctx, tx, now, grant, employee)
	}
	if typ == "GRANT" {
		to := grantStatus
		if grantStatus == "PROVISIONING" || grantStatus == "PENDING_FULFILLMENT" {
			to = "ACTIVE"
		}
		if err := setGrant(ctx, tx, grant, grantStatus, to, "activation_evidence = $3, effective_from = $4", evidence, now); err != nil {
			return err
		}
		if err := event("GRANT_ACTIVATED"); err != nil {
			return err
		}
		if to != "ACTIVE" {
			return staleIfTransferred(ctx, tx, now, actor, grant, employee)
		}
		if err := suspendIfSuspended(ctx, tx, now, grant, employee); err != nil {
			return err
		}
		var replaces *uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT replaces_grant_id FROM grants WHERE id = $1", grant).Scan(&replaces); err != nil || replaces == nil {
			return err
		}
		// The caller holds the employee's lock; the old grant's operations come next (see Runner).
		if _, err := tx.Exec(ctx, "SELECT 1 FROM operations WHERE grant_id = $1 FOR UPDATE", *replaces); err != nil {
			return err
		}
		// The system's request, for every method: the confirmer only confirmed the new level.
		return requestRemoval(ctx, tx, now, nil, *replaces, employee, "LEVEL_CHANGE", "level change")
	}
	// Access that was held ends now: activated, or imported (held since the import, D10).
	if err := setGrant(ctx, tx, grant, grantStatus, "REVOKED",
		"effective_to = CASE WHEN effective_from IS NOT NULL OR import_id IS NOT NULL THEN $3::timestamptz END", now); err != nil {
		return err
	}
	if err := endSuspension(ctx, tx, now, grant); err != nil {
		return err
	}
	return event("GRANT_REVOKED")
}

// staleIfTransferred marks a grant that activated after its transfer removal was requested:
// access the employee should no longer hold, an incident until removed (D5).
func staleIfTransferred(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, grant, employee uuid.UUID) error {
	tag, err := tx.Exec(ctx, `UPDATE grants SET stale_at = $2 WHERE id = $1 AND EXISTS (SELECT 1 FROM operations
		WHERE grant_id = $1 AND type = 'REVOKE' AND reason = 'TRANSFER' AND status IN ('PENDING', 'IN_PROGRESS', 'FAILED'))`, grant, now)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "STALE_GRANT_INCIDENT", Entity: "grant", EntityID: &grant,
		SubjectID: &employee})
}

// fail ends an IN_PROGRESS operation that did not work; it waits in Exceptions for a retry
// (O3). A GRANT failing while its removal is requested is cancelled instead: a retried
// GRANT could re-apply access after the removal.
func fail(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, op, grant, employee uuid.UUID,
	typ, grantStatus, msg string, details map[string]any) error {
	event := func(action string, details map[string]any) error {
		return audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: action, Entity: "operation", EntityID: &op, SubjectID: &employee,
			Details: details})
	}
	stale, err := unwanted(ctx, tx, op, grant, typ, grantStatus)
	if err != nil {
		return err
	}
	if stale || typ == "GRANT" && grantStatus == "REVOCATION_REQUESTED" {
		if err := setOp(ctx, tx, op, "IN_PROGRESS", "CANCELLED", "completed_at = $2, last_error = $3", now, msg); err != nil {
			return err
		}
		reason := "the grant is being removed"
		if stale && grantStatus != "REVOCATION_REQUESTED" && grantStatus != "REVOCATION_FAILED" {
			reason = "superseded"
		}
		return event("OPERATION_CANCELLED", map[string]any{"reason": reason})
	}
	if err := setOp(ctx, tx, op, "IN_PROGRESS", "FAILED", "completed_at = $2, last_error = $3", now, msg); err != nil {
		return err
	}
	// A failed SUSPEND or RESUME leaves the grant as it is: the tool's state is unknown, and
	// the operation waits in Exceptions (D7).
	if to, ok := map[string]string{"GRANT": "FAILED", "REVOKE": "REVOCATION_FAILED"}[typ]; ok {
		if err := setGrant(ctx, tx, grant, grantStatus, to, ""); err != nil {
			return err
		}
	}
	if details == nil {
		details = map[string]any{}
	}
	details["error"] = msg
	return event("OPERATION_FAILED", details)
}

// setOp moves a locked operation, clearing its lease; set may use $2… for args. Staying put is allowed.
func setOp(ctx context.Context, tx pgx.Tx, id uuid.UUID, from, to, set string, args ...any) error {
	if from != to && !canMove(opMoves, from, to) {
		return fmt.Errorf("operation %s → %s is not allowed", from, to)
	}
	_, err := tx.Exec(ctx, "UPDATE operations SET status = '"+to+"', lease_token = NULL, lease_until = NULL, version = version + 1, "+set+
		" WHERE id = $1", append([]any{id}, args...)...)
	return err
}

// setGrant moves a locked grant; set (optional) may use $3… for args. Staying put is allowed.
func setGrant(ctx context.Context, tx pgx.Tx, id uuid.UUID, from, to, set string, args ...any) error {
	if from != to && !canMove(grantMoves, from, to) {
		return fmt.Errorf("grant %s → %s is not allowed", from, to)
	}
	if set != "" {
		set = ", " + set
	}
	_, err := tx.Exec(ctx, "UPDATE grants SET status = $2, version = version + 1"+set+" WHERE id = $1", append([]any{id, to}, args...)...)
	return err
}

// errEmployeeBusy: another transaction holds the employee, and may be waiting for a lock we hold.
var errEmployeeBusy = errors.New("employee locked")

// cancelIfRetired cancels a grant never sent whose level or tool was retired since its
// approval (retired before provisioning), and reports whether it did. The cancellation
// needs the employee: the runner's claim holds only the operation, so a revoke holding the
// employee may be waiting for it; then nothing is done (errEmployeeBusy).
func cancelIfRetired(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, grant, employee uuid.UUID) (bool, error) {
	// Catalog retirement locks the tool: lock first, then read its status (A9).
	if _, err := tx.Exec(ctx, "SELECT 1 FROM tools WHERE id = (SELECT tool_id FROM grants WHERE id = $1) FOR SHARE", grant); err != nil {
		return false, err
	}
	var toolStatus, levelStatus string
	if err := tx.QueryRow(ctx, `SELECT t.status, l.status FROM grants g JOIN tools t ON t.id = g.tool_id
		JOIN access_levels l ON l.id = g.access_level_id WHERE g.id = $1`, grant).Scan(&toolStatus, &levelStatus); err != nil {
		return false, err
	}
	reason := RetiredReason(toolStatus, levelStatus)
	if reason == "" {
		return false, nil
	}
	tag, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE SKIP LOCKED", employee) // ours if we hold it
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, errEmployeeBusy
	}
	return true, cancelGrant(ctx, tx, now, actor, grant, employee, reason)
}

// cancelGrant cancels a grant that never reached the tool, with its open operations.
func cancelGrant(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, grant, employee uuid.UUID, reason string) error {
	if _, err := tx.Exec(ctx, `UPDATE operations SET status = 'CANCELLED', completed_at = $2, lease_token = NULL, lease_until = NULL,
		version = version + 1 WHERE grant_id = $1 AND status = 'PENDING'`, grant, now); err != nil {
		return err
	}
	var from string
	if err := tx.QueryRow(ctx, "SELECT status FROM grants WHERE id = $1 FOR UPDATE", grant).Scan(&from); err != nil {
		return err
	}
	if err := setGrant(ctx, tx, grant, from, "CANCELLED", "closed_reason = $3", reason); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "GRANT_CANCELLED", Entity: "grant", EntityID: &grant,
		SubjectID: &employee, Reason: reason})
}
