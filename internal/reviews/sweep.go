package reviews

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// LockKey makes one sweep at a time run the review steps.
const LockKey int64 = 0x0D8_4E71

// RunDue runs what is due (D8): this quarter's campaign, the closing of items whose grant
// has ended, and the active campaign's day-7, day-14 and day-21 steps. Each step runs once
// and catches up after downtime. It returns how many items or campaigns it changed.
// shortcut: one transaction per round; split per item if campaigns reach many thousands of grants.
func RunDue(ctx context.Context, pool *pgxpool.Pool, loc *time.Location) (int, error) {
	n := 0
	err := platform.InTx(ctx, pool, func(tx pgx.Tx, now time.Time) error {
		n = 0
		var mine bool
		if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", LockKey).Scan(&mine); err != nil || !mine {
			return err
		}
		for _, step := range []func(context.Context, pgx.Tx, time.Time, *time.Location) (int, error){quarterly, closeRemoved, steps} {
			changed, err := step(ctx, tx, now, loc)
			if err != nil {
				return err
			}
			n += changed
		}
		return nil
	})
	return n, err
}

// quarterly starts the quarter's campaign once, from the first quarter that begins after
// AccessFlow was installed, when no campaign is active.
func quarterly(ctx context.Context, tx pgx.Tx, now time.Time, loc *time.Location) (int, error) {
	key, begins := Quarter(now, loc)
	var due bool
	if err := tx.QueryRow(ctx, `SELECT (SELECT min(created_at) FROM employees) < $1
		AND NOT EXISTS (SELECT 1 FROM review_campaigns WHERE quarter = $2 OR closed_at IS NULL)`, begins, key).Scan(&due); err != nil || !due {
		return 0, err
	}
	_, err := start(ctx, tx, now, "QUARTERLY", &key, nil)
	return 1, err
}

// closeRemoved closes the open items, findings included, whose grant has been revoked or
// cancelled: there is nothing left to decide.
func closeRemoved(ctx context.Context, tx pgx.Tx, now time.Time, _ *time.Location) (int, error) {
	rows, err := tx.Query(ctx, `UPDATE review_items i SET closed_reason = 'ALREADY_REMOVED', closed_at = $1, version = i.version + 1
		FROM grants g WHERE g.id = i.grant_id AND g.status IN ('REVOKED', 'CANCELLED') AND `+open+`
		RETURNING i.id, g.employee_id, g.id`, now)
	if err != nil {
		return 0, err
	}
	type closed struct{ Item, Employee, Grant uuid.UUID }
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[closed])
	if err != nil {
		return 0, err
	}
	for _, c := range items {
		if err := audit.Record(ctx, tx, audit.Event{Action: "REVIEW_ITEM_CLOSED", Entity: "review_item", EntityID: &c.Item,
			SubjectID: &c.Employee, Reason: "already removed", Details: map[string]any{"grantId": c.Grant}}); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

// steps runs the active campaign's due day steps in order.
func steps(ctx context.Context, tx pgx.Tx, now time.Time, _ *time.Location) (int, error) {
	var id uuid.UUID
	var started time.Time
	var reminded, escalated bool
	err := tx.QueryRow(ctx, `SELECT id, started_at, reminded_at IS NOT NULL, escalated_at IS NOT NULL
		FROM review_campaigns WHERE closed_at IS NULL FOR UPDATE`).Scan(&id, &started, &reminded, &escalated)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	if !reminded && StepDue(started, now, 7) {
		if err := remind(ctx, tx, now, id); err != nil {
			return 0, err
		}
		n++
	}
	if !escalated && StepDue(started, now, 14) {
		if err := escalate(ctx, tx, now, id); err != nil {
			return 0, err
		}
		n++
	}
	if StepDue(started, now, 21) {
		closed, err := closeCampaign(ctx, tx, now, id)
		if err != nil {
			return 0, err
		}
		n += closed + 1
	}
	return n, nil
}

// openItems lists the campaign's undecided items, locked against a concurrent decision.
func openItems(ctx context.Context, tx pgx.Tx, campaign uuid.UUID) ([]openItem, error) {
	rows, err := tx.Query(ctx, `SELECT i.id, i.grant_id, g.employee_id, i.reviewer_id
		FROM review_items i JOIN grants g ON g.id = i.grant_id
		WHERE i.campaign_id = $1 AND `+open+` ORDER BY i.id FOR UPDATE OF i`, campaign)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[openItem])
}

type openItem struct {
	ID, Grant, Employee uuid.UUID
	Reviewer            *uuid.UUID
}

// remind sends day 7's reminder for every unanswered item: to its reviewer, or to InfoSec
// for a pool item.
func remind(ctx context.Context, tx pgx.Tx, now time.Time, campaign uuid.UUID) error {
	items, err := openItems(ctx, tx, campaign)
	if err != nil {
		return err
	}
	for _, it := range items {
		details := map[string]any{"campaignId": campaign, "grantId": it.Grant}
		if it.Reviewer == nil {
			err = notify.ToRole(ctx, tx, "INFOSEC", "REVIEW_REMINDER", "review_item", it.ID, details)
		} else {
			err = notify.ToEmployee(ctx, tx, *it.Reviewer, "REVIEW_REMINDER", "review_item", it.ID, details)
		}
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE review_campaigns SET reminded_at = $2 WHERE id = $1", campaign, now); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{Action: "REVIEW_REMINDERS_SENT", Entity: "review_campaign", EntityID: &campaign,
		Details: map[string]any{"items": len(items)}})
}

// escalate hands day 14's unanswered items to InfoSec, who may then decide any of them.
func escalate(ctx context.Context, tx pgx.Tx, now time.Time, campaign uuid.UUID) error {
	items, err := openItems(ctx, tx, campaign)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE review_campaigns SET escalated_at = $2 WHERE id = $1", campaign, now); err != nil {
		return err
	}
	details := map[string]any{"items": len(items)}
	if err := notify.ToRole(ctx, tx, "INFOSEC", "REVIEW_ESCALATED", "review_campaign", campaign, details); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{Action: "REVIEW_ESCALATED", Entity: "review_campaign", EntityID: &campaign, Details: details})
}

// closeCampaign ends the campaign on day 21: every unanswered item becomes an open finding,
// decided late or carried to later campaigns without being added again. A missed deadline
// never removes access (D8 note 2026-10-30); a leaver's access is removed by termination.
func closeCampaign(ctx context.Context, tx pgx.Tx, now time.Time, campaign uuid.UUID) (int, error) {
	items, err := openItems(ctx, tx, campaign)
	if err != nil {
		return 0, err
	}
	findings := 0
	for _, it := range items {
		// Removal locks first (see access.LockForRemoval); a grant already being removed is
		// left open and closes as already removed when its removal completes.
		if err := access.LockForRemoval(ctx, tx, it.Grant); err != nil {
			return 0, err
		}
		if held, err := reviewable(ctx, tx, it.Grant); err != nil || !held {
			if err != nil {
				return 0, err
			}
			continue
		}
		findings++
		if _, err := tx.Exec(ctx, "UPDATE review_items SET finding_at = $2, version = version + 1 WHERE id = $1", it.ID, now); err != nil {
			return 0, err
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "REVIEW_FINDING_OPENED", Entity: "review_item", EntityID: &it.ID,
			SubjectID: &it.Employee, Details: map[string]any{"grantId": it.Grant}}); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(ctx, "UPDATE review_campaigns SET closed_at = $2 WHERE id = $1", campaign, now); err != nil {
		return 0, err
	}
	return len(items), audit.Record(ctx, tx, audit.Event{Action: "REVIEW_CAMPAIGN_CLOSED", Entity: "review_campaign", EntityID: &campaign,
		Details: map[string]any{"findings": findings}})
}
