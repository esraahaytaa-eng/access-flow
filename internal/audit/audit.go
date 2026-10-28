// Package audit writes the append-only audit history (A9).
package audit

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Event is one audit record. A nil ActorID means the system acted.
type Event struct {
	ActorID   *uuid.UUID
	Action    string
	Entity    string
	EntityID  *uuid.UUID
	SubjectID *uuid.UUID // the employee whose access or record is affected
	Reason    string
	Details   map[string]any
}

// Record writes e inside tx, so it commits or rolls back with the state change.
// The database sets occurred_at to the transaction time.
func Record(ctx context.Context, tx pgx.Tx, e Event) error {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_id, action, entity_type, entity_id, subject_employee_id, reason, details)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7)`,
		e.ActorID, e.Action, e.Entity, e.EntityID, e.SubjectID, e.Reason, details)
	if err != nil {
		return fmt.Errorf("record audit event %s: %w", e.Action, err)
	}
	return nil
}
