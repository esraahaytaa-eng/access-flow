package reviews

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
)

// OwnerChanged moves the tool's undecided review items, open findings included, to its new
// owner, in the owner change's transaction, which holds the tool's lock (D8 note 2026-10-31).
// The Tool Owner decides (D8): the former owner no longer does. Reviewer routes each item as
// at campaign start, so the new owner's own access goes to InfoSec. Decided items keep their
// reviewer and decision. Each item is locked and re-read, so a decision committed meanwhile
// wins and that item is left as it is.
func OwnerChanged(ctx context.Context, tx pgx.Tx, actor *uuid.UUID, tool, owner uuid.UUID) error {
	o := Owner{ID: owner, Active: true} // the catalog accepts only an active owner
	var name string
	if err := tx.QueryRow(ctx, `SELECT name, EXISTS (SELECT 1 FROM role_assignments WHERE employee_id = $2 AND role = 'SUPER_ADMIN' AND valid_to IS NULL)
		FROM tools WHERE id = $1`, tool, owner).Scan(&name, &o.SuperAdmin); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT i.id, i.campaign_id, g.employee_id, i.reviewer_id FROM review_items i JOIN grants g ON g.id = i.grant_id
		WHERE g.tool_id = $1 AND `+open+` ORDER BY i.id FOR UPDATE OF i`, tool)
	if err != nil {
		return err
	}
	type item struct {
		ID, Campaign, Employee uuid.UUID
		From                   *uuid.UUID
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[item])
	if err != nil {
		return err
	}
	moved := map[uuid.UUID]int{} // items now reviewed by the new owner, per campaign
	for _, it := range items {
		reviewer, note := Reviewer(o, it.Employee)
		if _, err := tx.Exec(ctx, `UPDATE review_items SET reviewer_id = $2, routing_note = nullif($3, ''), version = version + 1 WHERE id = $1`,
			it.ID, reviewer, note); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "REVIEW_ITEM_REROUTED", Entity: "review_item", EntityID: &it.ID,
			SubjectID: &it.Employee, Details: map[string]any{"reason": "tool owner changed", "toolId": tool,
				"fromReviewerId": it.From, "toReviewerId": reviewer}}); err != nil {
			return err
		}
		if reviewer != nil {
			moved[it.Campaign]++
		}
	}
	for campaign, n := range moved { // the new owner learns once per campaign, as reviewers do at its start
		if err := notify.ToEmployee(ctx, tx, owner, "REVIEW_STARTED", "review_campaign", campaign,
			map[string]any{"items": n, "tool": name, "toolId": tool, "toolOwnerChanged": true}); err != nil {
			return err
		}
	}
	return nil
}
