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
			  AND NOT (o.type = 'REVOKE' AND EXISTS (SELECT 1 FROM operations x
			           WHERE x.grant_id = o.grant_id AND x.type = 'GRANT' AND x.status IN ('PENDING', 'IN_PROGRESS')))
			ORDER BY o.priority, o.created_at LIMIT 1 FOR UPDATE OF o SKIP LOCKED`, now).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		// Catalog retirement locks the tool: lock first, then read its status (A9).
		if _, err := tx.Exec(ctx, `SELECT 1 FROM tools WHERE id = (SELECT g.tool_id FROM operations o JOIN grants g ON g.id = o.grant_id
			WHERE o.id = $1) FOR SHARE`, id); err != nil {
			return err
		}
		next := claimed{op: id, token: uuid.New()}
		var neverSent bool
		var toolStatus, levelStatus string
		if err := tx.QueryRow(ctx, `SELECT o.type, o.attempts + 1, o.first_attempt_at IS NULL, g.id, g.employee_id, e.email,
			       g.tool_id, g.access_level_id, t.status, l.status
			FROM operations o JOIN grants g ON g.id = o.grant_id JOIN employees e ON e.id = g.employee_id
			JOIN tools t ON t.id = g.tool_id JOIN access_levels l ON l.id = g.access_level_id WHERE o.id = $1`, id).
			Scan(&next.typ, &next.attempts, &neverSent, &next.grant, &next.employee, &next.account,
				&next.tool, &next.level, &toolStatus, &levelStatus); err != nil {
			return err
		}
		if reason := RetiredReason(toolStatus, levelStatus); next.typ == "GRANT" && neverSent && reason != "" {
			return cancelGrant(ctx, tx, now, nil, next.grant, next.employee, reason)
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
	}
	return nil
}

// record writes the outcome, unless the lease was lost: then the worker that re-claimed
// the operation owns the result.
func (r *Runner) record(ctx context.Context, c claimed, callErr error) error {
	return platform.InTx(ctx, r.pool, func(tx pgx.Tx, now time.Time) error {
		tag, err := tx.Exec(ctx, "SELECT 1 FROM operations WHERE id = $1 AND lease_token = $2 AND status = 'IN_PROGRESS' FOR UPDATE",
			c.op, c.token)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		var grantStatus string
		if err := tx.QueryRow(ctx, "SELECT status FROM grants WHERE id = $1 FOR UPDATE", c.grant).Scan(&grantStatus); err != nil {
			return err
		}
		event := func(action string, entity string, id uuid.UUID, details map[string]any) error {
			return audit.Record(ctx, tx, audit.Event{Action: action, Entity: entity, EntityID: &id, SubjectID: &c.employee, Details: details})
		}
		if callErr == nil {
			return r.succeeded(ctx, tx, now, c, grantStatus, event)
		}

		msg := strings.ToValidUTF8(callErr.Error()[:min(len(callErr.Error()), 500)], "")
		if err := event("OPERATION_ATTEMPT_FAILED", "operation", c.op, map[string]any{"attempt": c.attempts, "error": msg}); err != nil {
			return err
		}
		wait, final := Backoff(c.attempts)
		var rejected *StatusError
		if errors.As(callErr, &rejected) && rejected.Status == http.StatusConflict {
			final = true // the tool refused the operation: retrying cannot help (O3)
		}
		switch {
		case c.typ == "GRANT" && grantStatus == "REVOCATION_REQUESTED":
			// The removal takes over; a retried GRANT could re-apply access after it.
			if err := setOp(ctx, tx, c.op, "IN_PROGRESS", "CANCELLED", "completed_at = $2, last_error = $3", now, msg); err != nil {
				return err
			}
			return event("OPERATION_CANCELLED", "operation", c.op, map[string]any{"reason": "the grant is being removed"})
		case !final:
			return setOp(ctx, tx, c.op, "IN_PROGRESS", "PENDING", "next_attempt_at = $2, last_error = $3", now.Add(wait), msg)
		}
		if err := setOp(ctx, tx, c.op, "IN_PROGRESS", "FAILED", "completed_at = $2, last_error = $3", now, msg); err != nil {
			return err
		}
		to := map[string]string{"GRANT": "FAILED", "REVOKE": "REVOCATION_FAILED"}[c.typ]
		if err := setGrant(ctx, tx, c.grant, grantStatus, to, ""); err != nil {
			return err
		}
		return event("OPERATION_FAILED", "operation", c.op, map[string]any{"attempts": c.attempts, "error": msg})
	})
}

// succeeded records a verified result. A GRANT that completes after its removal was
// requested records when access began and leaves the removal in charge (D5).
func (r *Runner) succeeded(ctx context.Context, tx pgx.Tx, now time.Time, c claimed, grantStatus string,
	event func(string, string, uuid.UUID, map[string]any) error) error {
	if err := setOp(ctx, tx, c.op, "IN_PROGRESS", "SUCCEEDED", "completed_at = $2, last_error = NULL", now); err != nil {
		return err
	}
	if c.typ == "GRANT" {
		to := grantStatus
		if grantStatus == "PROVISIONING" {
			to = "ACTIVE"
		}
		if err := setGrant(ctx, tx, c.grant, grantStatus, to, "activation_evidence = 'VERIFIED', effective_from = $3", now); err != nil {
			return err
		}
		return event("GRANT_ACTIVATED", "grant", c.grant, map[string]any{"operationId": c.op, "evidence": "VERIFIED"})
	}
	if err := setGrant(ctx, tx, c.grant, grantStatus, "REVOKED", "effective_to = CASE WHEN effective_from IS NOT NULL THEN $3::timestamptz END", now); err != nil {
		return err
	}
	return event("GRANT_REVOKED", "grant", c.grant, map[string]any{"operationId": c.op, "evidence": "VERIFIED"})
}

// setOp moves a locked operation, clearing its lease; set may use $2… for args.
func setOp(ctx context.Context, tx pgx.Tx, id uuid.UUID, from, to, set string, args ...any) error {
	if !canMove(opMoves, from, to) {
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
