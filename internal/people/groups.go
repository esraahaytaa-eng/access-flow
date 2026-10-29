package people

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var errGroupNameTaken = platform.NewError(http.StatusConflict, "group_name_taken")

// AdminGroup is an admin group; MemberIDs is set only for the directory roles.
type AdminGroup struct {
	ID        uuid.UUID    `json:"id"`
	Name      string       `json:"name"`
	MemberIDs *[]uuid.UUID `json:"memberIds,omitempty"` // a pointer, so an empty group still shows []
}

// CreateGroup creates an admin group (Super Admin only; names unique regardless of case).
func (s *Service) CreateGroup(ctx context.Context, actor auth.Principal, name string) (AdminGroup, error) {
	if !actor.HasRole("SUPER_ADMIN") {
		return AdminGroup{}, platform.ErrForbidden
	}
	name, err := cleanGroupName(name)
	if err != nil {
		return AdminGroup{}, err
	}
	g := AdminGroup{Name: name}
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, _ time.Time) error {
		err := tx.QueryRow(ctx, "INSERT INTO admin_groups (name, created_by) VALUES ($1, $2) RETURNING id", name, actorRef(actor)).Scan(&g.ID)
		if isUniqueViolation(err) {
			return errGroupNameTaken
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "ADMIN_GROUP_CREATED", Entity: "admin_group",
			EntityID: &g.ID, Details: map[string]any{"name": name}})
	})
	return g, err
}

// ListGroups returns every group by name, with current members for the directory roles.
func (s *Service) ListGroups(ctx context.Context, actor auth.Principal) ([]AdminGroup, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.name,
		       ARRAY(SELECT m.employee_id FROM admin_group_memberships m WHERE m.group_id = g.id AND m.valid_to IS NULL ORDER BY m.employee_id)
		FROM admin_groups g ORDER BY lower(g.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []AdminGroup{}
	full := seesFullDirectory(actor)
	for rows.Next() {
		var g AdminGroup
		var ids []uuid.UUID
		if err := rows.Scan(&g.ID, &g.Name, &ids); err != nil {
			return nil, err
		}
		if full {
			g.MemberIDs = &ids
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// AddMember opens a membership for an active employee; already a member is a no-op.
func (s *Service) AddMember(ctx context.Context, actor auth.Principal, group, id uuid.UUID) error {
	if err := checkMembershipChange(actor, id); err != nil {
		return err
	}
	return platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		name, err := groupName(ctx, tx, group)
		if err != nil {
			return err
		}
		target, err := LockEmployee(ctx, tx, id)
		if err != nil {
			return err
		}
		if target.Status != "ACTIVE" {
			return errNotActive
		}
		tag, err := tx.Exec(ctx, `INSERT INTO admin_group_memberships (group_id, employee_id, valid_from, granted_by)
			SELECT $1, $2, $3, $4 WHERE NOT EXISTS (
				SELECT 1 FROM admin_group_memberships WHERE group_id = $1 AND employee_id = $2 AND valid_to IS NULL)`,
			group, id, now, actorRef(actor))
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "ADMIN_GROUP_MEMBER_ADDED", Entity: "admin_group",
			EntityID: &group, SubjectID: &id, Details: map[string]any{"group": name}})
	})
}

// RemoveMember closes the open membership (the row is kept); not a member is a no-op.
func (s *Service) RemoveMember(ctx context.Context, actor auth.Principal, group, id uuid.UUID) error {
	if err := checkMembershipChange(actor, id); err != nil {
		return err
	}
	return platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		name, err := groupName(ctx, tx, group)
		if err != nil {
			return err
		}
		if _, err := LockEmployee(ctx, tx, id); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE admin_group_memberships SET valid_to = GREATEST(valid_from, $3), revoked_by = $4
			WHERE group_id = $1 AND employee_id = $2 AND valid_to IS NULL`, group, id, now, actorRef(actor))
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "ADMIN_GROUP_MEMBER_REMOVED", Entity: "admin_group",
			EntityID: &group, SubjectID: &id, Details: map[string]any{"group": name}})
	})
}

func groupName(ctx context.Context, tx pgx.Tx, id uuid.UUID) (string, error) {
	var name string
	err := tx.QueryRow(ctx, "SELECT name FROM admin_groups WHERE id = $1", id).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", platform.ErrNotFound
	}
	return name, err
}
