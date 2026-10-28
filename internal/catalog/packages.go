package catalog

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var (
	errLevelNotFound       = platform.ValidationError("accessLevelId", "level_not_found")
	errLevelNotRequestable = platform.NewError(http.StatusConflict, "level_not_requestable")
	errPackageEntryExists  = platform.NewError(http.StatusConflict, "package_entry_exists")
)

const maxTextLength = 100 // department, job title: as on employees

// Person names an employee in the catalog.
type Person struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"fullName"`
}

// CatalogTool is a tool as the catalog shows it. Status and Version are empty (and
// omitted) for viewers without the full catalog.
type CatalogTool struct {
	ID                uuid.UUID      `json:"id"`
	Name              string         `json:"name"`
	FulfillmentMethod string         `json:"fulfillmentMethod"`
	Owner             Person         `json:"owner"`
	AdminGroup        auth.Group     `json:"adminGroup"`
	Status            string         `json:"status,omitempty"`
	Version           int            `json:"version,omitempty"`
	Levels            []CatalogLevel `json:"levels"`
}

// CatalogLevel is a level as the catalog shows it; Recommended marks the viewer's starter
// package (F3), which recommends and never restricts.
type CatalogLevel struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Steps       []string  `json:"steps"`
	Status      string    `json:"status,omitempty"`
	Version     int       `json:"version,omitempty"`
	Requestable bool      `json:"requestable"`
	Recommended bool      `json:"recommended"`
}

// Catalog lists the tools and levels the viewer may see: requestable ones for everyone;
// retired ones too, with versions, for Super Admin, InfoSec and Auditor.
func (s *Service) Catalog(ctx context.Context, viewer auth.Principal) ([]CatalogTool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.name, t.fulfillment_method, t.status, t.version, o.id, o.full_name, g.id, g.name
		FROM tools t JOIN employees o ON o.id = t.owner_id JOIN admin_groups g ON g.id = t.admin_group_id
		ORDER BY lower(t.name)`)
	if err != nil {
		return nil, err
	}
	tools, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (CatalogTool, error) {
		var t CatalogTool
		err := row.Scan(&t.ID, &t.Name, &t.FulfillmentMethod, &t.Status, &t.Version, &t.Owner.ID, &t.Owner.FullName,
			&t.AdminGroup.ID, &t.AdminGroup.Name)
		return t, err
	})
	if err != nil {
		return nil, err
	}
	rows, err = s.pool.Query(ctx, `
		WITH me AS (SELECT department, job_title FROM employees WHERE id = $1)
		SELECT l.tool_id, l.id, l.name, l.requires_tool_owner, l.requires_infosec, l.status, l.version,
		       l.status = 'ACTIVE' AND t.status = 'ACTIVE',
		       EXISTS (SELECT 1 FROM starter_packages p, me
		               WHERE p.access_level_id = l.id AND p.removed_at IS NULL
		                 AND lower(p.department) = lower(me.department)
		                 AND (p.job_title IS NULL OR lower(p.job_title) = lower(me.job_title)))
		FROM access_levels l JOIN tools t ON t.id = l.tool_id
		ORDER BY lower(l.name)`, viewer.EmployeeID)
	if err != nil {
		return nil, err
	}
	byTool := map[uuid.UUID][]CatalogLevel{}
	var toolID uuid.UUID
	var l CatalogLevel
	var owner, infosec bool
	_, err = pgx.ForEachRow(rows, []any{&toolID, &l.ID, &l.Name, &owner, &infosec, &l.Status, &l.Version, &l.Requestable, &l.Recommended},
		func() error {
			l.Steps = Steps(owner, infosec)
			byTool[toolID] = append(byTool[toolID], l)
			return nil
		})
	if err != nil {
		return nil, err
	}

	full := seesFullCatalog(viewer)
	out := []CatalogTool{}
	for _, t := range tools {
		for _, l := range byTool[t.ID] {
			if full {
				t.Levels = append(t.Levels, l)
			} else if l.Requestable {
				l.Status, l.Version = "", 0
				t.Levels = append(t.Levels, l)
			}
		}
		if full {
			if t.Levels == nil {
				t.Levels = []CatalogLevel{}
			}
			out = append(out, t)
		} else if len(t.Levels) > 0 {
			t.Status, t.Version = "", 0
			out = append(out, t)
		}
	}
	return out, nil
}

// PackageEntry is one open starter package entry.
type PackageEntry struct {
	ID            uuid.UUID `json:"id"`
	Department    string    `json:"department"`
	JobTitle      *string   `json:"jobTitle"`
	AccessLevelID uuid.UUID `json:"accessLevelId"`
	Tool          string    `json:"tool"`
	Level         string    `json:"level"`
	Requestable   bool      `json:"requestable"` // false once the level or its tool is retired
}

// NewPackage is the input for AddPackage; a nil JobTitle covers the whole department.
type NewPackage struct {
	Department    string
	JobTitle      *string
	AccessLevelID uuid.UUID
}

const selectPackages = `
	SELECT p.id, p.department, p.job_title, p.access_level_id, t.name, l.name, l.status = 'ACTIVE' AND t.status = 'ACTIVE'
	FROM starter_packages p JOIN access_levels l ON l.id = p.access_level_id JOIN tools t ON t.id = l.tool_id`

func scanPackage(row pgx.Row) (PackageEntry, error) {
	var p PackageEntry
	err := row.Scan(&p.ID, &p.Department, &p.JobTitle, &p.AccessLevelID, &p.Tool, &p.Level, &p.Requestable)
	return p, err
}

// ListPackages lists the open entries; recommendations are not secret.
func (s *Service) ListPackages(ctx context.Context) ([]PackageEntry, error) {
	rows, err := s.pool.Query(ctx, selectPackages+` WHERE p.removed_at IS NULL
		ORDER BY lower(p.department), lower(coalesce(p.job_title, '')), lower(t.name), lower(l.name)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (PackageEntry, error) { return scanPackage(row) })
}

// AddPackage recommends a requestable level to a department, optionally one job title.
func (s *Service) AddPackage(ctx context.Context, actor auth.Principal, in NewPackage) (PackageEntry, error) {
	if err := canEdit(actor); err != nil {
		return PackageEntry{}, err
	}
	dept := strings.TrimSpace(in.Department)
	if dept == "" {
		return PackageEntry{}, platform.ValidationError("department", "department_required")
	}
	if err := platform.CheckText(dept, "department", maxTextLength); err != nil {
		return PackageEntry{}, err
	}
	var title *string
	if in.JobTitle != nil {
		if v := strings.TrimSpace(*in.JobTitle); v != "" {
			if err := platform.CheckText(v, "jobTitle", maxTextLength); err != nil {
				return PackageEntry{}, err
			}
			title = &v
		}
	}
	var entry PackageEntry
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		var toolID uuid.UUID // a level never moves to another tool
		err := tx.QueryRow(ctx, "SELECT tool_id FROM access_levels WHERE id = $1", in.AccessLevelID).Scan(&toolID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errLevelNotFound
		}
		if err != nil {
			return err
		}
		tool, err := lockTool(ctx, tx, toolID) // holds off a concurrent retirement
		if err != nil {
			return err
		}
		level, err := getLevel(ctx, tx, toolID, in.AccessLevelID)
		if err != nil {
			return err
		}
		if tool.Status != "ACTIVE" || level.Status != "ACTIVE" {
			return errLevelNotRequestable
		}
		var id uuid.UUID
		err = tx.QueryRow(ctx, `INSERT INTO starter_packages (department, job_title, access_level_id, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, dept, title, in.AccessLevelID, actorRef(actor), now).Scan(&id)
		if isUnique(err) {
			return errPackageEntryExists
		}
		if err != nil {
			return err
		}
		entry = PackageEntry{ID: id, Department: dept, JobTitle: title, AccessLevelID: in.AccessLevelID, Tool: tool.Name, Level: level.Name, Requestable: true}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "STARTER_PACKAGE_ENTRY_ADDED", Entity: "starter_package",
			EntityID: &id, Details: map[string]any{"department": dept, "jobTitle": title, "tool": tool.Name, "level": level.Name}})
	})
	return entry, err
}

// RemovePackage closes an entry (the row is kept); an entry already removed is a no-op.
func (s *Service) RemovePackage(ctx context.Context, actor auth.Principal, id uuid.UUID) error {
	if err := canEdit(actor); err != nil {
		return err
	}
	return platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		entry, err := scanPackage(tx.QueryRow(ctx, selectPackages+" WHERE p.id = $1 FOR UPDATE OF p", id))
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrNotFound
		}
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, "UPDATE starter_packages SET removed_at = $2, removed_by = $3 WHERE id = $1 AND removed_at IS NULL",
			id, now, actorRef(actor))
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "STARTER_PACKAGE_ENTRY_REMOVED", Entity: "starter_package",
			EntityID: &id, Details: map[string]any{"department": entry.Department, "jobTitle": entry.JobTitle, "tool": entry.Tool, "level": entry.Level}})
	})
}

func (s *Service) handleCatalog(w http.ResponseWriter, r *http.Request) {
	tools, err := s.Catalog(r.Context(), actor(r))
	platform.Respond(w, r, http.StatusOK, map[string]any{"tools": tools}, err)
}

func (s *Service) handleListPackages(w http.ResponseWriter, r *http.Request) {
	entries, err := s.ListPackages(r.Context())
	platform.Respond(w, r, http.StatusOK, map[string]any{"entries": entries}, err)
}

func (s *Service) handleAddPackage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Department, JobTitle *string
		AccessLevelID        *uuid.UUID
	}
	if err := platform.DecodeRequired(w, r, &in, func() []platform.Field {
		return []platform.Field{{Name: "department", Present: in.Department != nil}, {Name: "accessLevelId", Present: in.AccessLevelID != nil}}
	}); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	e, err := s.AddPackage(r.Context(), actor(r), NewPackage{*in.Department, in.JobTitle, *in.AccessLevelID})
	platform.Respond(w, r, http.StatusCreated, e, err)
}

func (s *Service) handleRemovePackage(w http.ResponseWriter, r *http.Request) {
	ids, err := platform.PathIDs(r, "id")
	if err == nil {
		err = s.RemovePackage(r.Context(), actor(r), ids[0])
	}
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
