package people

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Entry is a department or job position the company defined (F3, M17). It grants nothing.
type Entry struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Active bool      `json:"active"`
}

// CompanyCatalog is the company's departments and job positions, by name.
type CompanyCatalog struct {
	Departments  []Entry `json:"departments"`
	JobPositions []Entry `json:"jobPositions"`
}

// catalogList is one of the two lists: its table, audit action prefix and error field.
type catalogList struct{ table, action, field string }

var (
	departments  = catalogList{"departments", "DEPARTMENT", "department"}
	jobPositions = catalogList{"job_positions", "JOB_POSITION", "jobTitle"}
	catalogLists = map[string]catalogList{"departments": departments, "job-positions": jobPositions}
)

// Catalog lists both lists, active and retired, for everyone signed in (the forms' pickers).
func (s *Service) Catalog(ctx context.Context) (CompanyCatalog, error) {
	var c CompanyCatalog
	var err error
	if c.Departments, err = s.entries(ctx, departments); err == nil {
		c.JobPositions, err = s.entries(ctx, jobPositions)
	}
	return c, err
}

func (s *Service) entries(ctx context.Context, l catalogList) ([]Entry, error) {
	rows, err := s.pool.Query(ctx, "SELECT id, name, active FROM "+l.table+" ORDER BY lower(name), name")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Entry])
}

// AddEntry defines a department or job position (HR Manager, Super Admin). A name that
// differs from an existing one only by case is refused with the defined spelling.
func (s *Service) AddEntry(ctx context.Context, actor auth.Principal, list string, name string) (Entry, error) {
	l, ok := catalogLists[list]
	if !ok {
		return Entry{}, platform.ErrNotFound
	}
	if !canManageEmployees(actor) {
		return Entry{}, platform.ErrForbidden
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Entry{}, platform.ValidationError("name", "name_required")
	}
	if err := platform.CheckText(name, "name", maxTextLength); err != nil {
		return Entry{}, err
	}
	e := Entry{Name: name, Active: true}
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		// The case-insensitive check is the service's (the key is the exact name): one at a time.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", l.table); err != nil {
			return err
		}
		var taken string
		err := tx.QueryRow(ctx, "SELECT name FROM "+l.table+" WHERE lower(name) = lower($1) LIMIT 1", name).Scan(&taken)
		if err == nil {
			return &platform.Error{Status: http.StatusConflict, Code: "name_taken", Field: "name", Expected: taken}
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(ctx, "INSERT INTO "+l.table+" (name, created_by, created_at) VALUES ($1, $2, $3) RETURNING id",
			name, actorRef(actor), now).Scan(&e.ID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: l.action + "_CREATED", Entity: l.table,
			EntityID: &e.ID, Details: map[string]any{"name": name}})
	})
	return e, err
}

// RetireEntry stops new use of an entry; whoever holds it keeps it. Repeating it is a no-op.
func (s *Service) RetireEntry(ctx context.Context, actor auth.Principal, list string, id uuid.UUID) (Entry, error) {
	l, ok := catalogLists[list]
	if !ok {
		return Entry{}, platform.ErrNotFound
	}
	if !canManageEmployees(actor) {
		return Entry{}, platform.ErrForbidden
	}
	var e Entry
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, _ time.Time) error {
		err := tx.QueryRow(ctx, "SELECT id, name, active FROM "+l.table+" WHERE id = $1 FOR UPDATE", id).Scan(&e.ID, &e.Name, &e.Active)
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrNotFound
		}
		if err != nil || !e.Active {
			return err
		}
		e.Active = false
		if _, err := tx.Exec(ctx, "UPDATE "+l.table+" SET active = false, version = version + 1 WHERE id = $1", id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: l.action + "_RETIRED", Entity: l.table,
			EntityID: &id, Details: map[string]any{"name": e.Name}})
	})
	return e, err
}

// CheckPlacement requires a department and job position that changed from the current
// values (nil for a new employee) to be active catalog entries, named exactly. It locks
// them FOR SHARE, so they cannot be retired before the caller's transaction commits.
func CheckPlacement(ctx context.Context, tx pgx.Tx, curDepartment, curJobTitle, department, jobTitle *string) error {
	if !SameText(curDepartment, department) {
		if err := checkEntry(ctx, tx, departments, department); err != nil {
			return err
		}
	}
	if !SameText(curJobTitle, jobTitle) {
		return checkEntry(ctx, tx, jobPositions, jobTitle)
	}
	return nil
}

func checkEntry(ctx context.Context, tx pgx.Tx, l catalogList, name *string) error {
	if name == nil {
		return nil
	}
	kind := strings.ToLower(l.action)
	var active bool
	err := tx.QueryRow(ctx, "SELECT active FROM "+l.table+" WHERE name = $1 FOR SHARE", *name).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		e := platform.ValidationError(l.field, "unknown_"+kind)
		// A case-only difference names the defined spelling, so the fix is obvious.
		_ = tx.QueryRow(ctx, "SELECT name FROM "+l.table+" WHERE lower(name) = lower($1) AND active ORDER BY name LIMIT 1", *name).Scan(&e.Expected)
		return e
	}
	if err != nil {
		return err
	}
	if !active {
		return platform.ValidationError(l.field, "retired_"+kind)
	}
	return nil
}

func (s *Service) handleCatalog(w http.ResponseWriter, r *http.Request) {
	c, err := s.Catalog(r.Context())
	platform.Respond(w, r, http.StatusOK, c, err)
}

func (s *Service) handleAddEntry(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	err := platform.DecodeJSON(w, r, &in)
	var e Entry
	if err == nil {
		e, err = s.AddEntry(r.Context(), actor(r), chi.URLParam(r, "list"), in.Name)
	}
	platform.Respond(w, r, http.StatusCreated, e, err)
}

func (s *Service) handleRetireEntry(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	var e Entry
	if err == nil {
		e, err = s.RetireEntry(r.Context(), actor(r), chi.URLParam(r, "list"), id)
	}
	platform.Respond(w, r, http.StatusOK, e, err)
}
