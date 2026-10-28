// Package notify writes and lists in-app notifications (O2). It is a leaf: other packages
// call it inside their own transactions, so a notification commits with its state change.
package notify

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Notification is one in-app message for one recipient.
type Notification struct {
	ID        uuid.UUID      `json:"id"`
	Kind      string         `json:"kind"`
	Entity    string         `json:"entity"`
	EntityID  *uuid.UUID     `json:"entityId"`
	Details   map[string]any `json:"details"`
	CreatedAt time.Time      `json:"createdAt"`
	ReadAt    *time.Time     `json:"readAt"`
}

// ToRole notifies every ACTIVE employee who currently holds role, inside tx.
func ToRole(ctx context.Context, tx pgx.Tx, role, kind, entity string, entityID uuid.UUID, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO notifications (recipient_id, kind, entity, entity_id, details)
		SELECT e.id, $2, $3, $4, $5 FROM employees e
		WHERE e.status = 'ACTIVE'
		  AND EXISTS (SELECT 1 FROM role_assignments r WHERE r.employee_id = e.id AND r.role = $1 AND r.valid_to IS NULL)`,
		role, kind, entity, entityID, details)
	return err
}

// ToEmployee notifies one employee, inside tx.
func ToEmployee(ctx context.Context, tx pgx.Tx, recipient uuid.UUID, kind, entity string, entityID uuid.UUID, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	_, err := tx.Exec(ctx, `INSERT INTO notifications (recipient_id, kind, entity, entity_id, details) VALUES ($1, $2, $3, $4, $5)`,
		recipient, kind, entity, entityID, details)
	return err
}

// List returns the recipient's newest 100 notifications.
func List(ctx context.Context, pool *pgxpool.Pool, recipient uuid.UUID) ([]Notification, error) {
	rows, err := pool.Query(ctx, `SELECT id, kind, entity, entity_id, details, created_at, read_at FROM notifications
		WHERE recipient_id = $1 ORDER BY created_at DESC, id DESC LIMIT 100`, recipient)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Notification, error) {
		var n Notification
		err := row.Scan(&n.ID, &n.Kind, &n.Entity, &n.EntityID, &n.Details, &n.CreatedAt, &n.ReadAt)
		return n, err
	})
}

// MarkRead marks the recipient's own notification read; someone else's is not found.
func MarkRead(ctx context.Context, pool *pgxpool.Pool, recipient, id uuid.UUID) error {
	tag, err := pool.Exec(ctx, "UPDATE notifications SET read_at = COALESCE(read_at, now()) WHERE id = $1 AND recipient_id = $2",
		id, recipient)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// Register adds the notification routes to r, which must already require a session.
func Register(r chi.Router, pool *pgxpool.Pool) {
	r.Get("/notifications", func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.PrincipalFrom(r.Context())
		list, err := List(r.Context(), pool, p.EmployeeID)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, map[string]any{"notifications": list})
	})
	r.Post("/notifications/{id}/read", func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.PrincipalFrom(r.Context())
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err == nil {
			err = MarkRead(r.Context(), pool, p.EmployeeID, id)
		} else {
			err = platform.ErrNotFound
		}
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
