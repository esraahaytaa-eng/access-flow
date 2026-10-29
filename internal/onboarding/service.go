package onboarding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/catalog"
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// maxUpload caps the uploaded workbook; 400 employees, 60 tools and their access are far less.
const maxUpload = 2 << 20

const xlsxType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

var (
	errWorkbookInvalid = platform.NewError(http.StatusBadRequest, "workbook_invalid")
	errFileTooLarge    = platform.NewError(http.StatusRequestEntityTooLarge, "file_too_large")
)

// Service serves the onboarding workbook: template, preview and import (Super Admin only).
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func canOnboard(actor auth.Principal) error {
	if !actor.HasRole("SUPER_ADMIN") {
		return platform.ErrForbidden
	}
	return nil
}

// TemplateFile builds the template from the current catalog.
func (s *Service) TemplateFile(ctx context.Context, actor auth.Principal) ([]byte, error) {
	if err := canOnboard(actor); err != nil {
		return nil, err
	}
	snap, err := s.snapshot(ctx, false)
	if err != nil {
		return nil, err
	}
	return Template(snap)
}

// Preview validates the workbook and writes nothing.
func (s *Service) Preview(ctx context.Context, actor auth.Principal, data []byte) (Plan, error) {
	if err := canOnboard(actor); err != nil {
		return Plan{}, err
	}
	wb, problems := Parse(data)
	if len(problems) > 0 {
		return Plan{Owners: []Owner{}, Problems: problems, Warnings: []Problem{}}, nil
	}
	snap, err := s.snapshot(ctx, false)
	if err != nil {
		return Plan{}, err
	}
	return Validate(wb, snap), nil
}

// Result is what an import did.
type Result struct {
	Plan   Plan                 `json:"plan"`
	Report *access.ImportReport `json:"report,omitempty"`
}

// Import validates the workbook again inside one transaction and applies it in dependency
// order: employees (managers first), tools and levels, then existing access through the
// D10 code. Any problem or failure writes nothing.
func (s *Service) Import(ctx context.Context, actor auth.Principal, fileName string, data []byte) (Result, error) {
	if err := canOnboard(actor); err != nil {
		return Result{}, err
	}
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return Result{}, platform.ValidationError("fileName", "field_required")
	}
	if err := platform.CheckText(fileName, "fileName", 200); err != nil {
		return Result{}, err
	}
	wb, problems := Parse(data)
	if len(problems) > 0 {
		return Result{Plan: Plan{Owners: []Owner{}, Problems: problems, Warnings: []Problem{}}}, errWorkbookInvalid
	}
	var res Result
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		// One import at a time, shared with the D10 tool read and CSV imports.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", access.ImportLockKey); err != nil {
			return err
		}
		snap, err := load(ctx, tx, true)
		if err != nil {
			return err
		}
		res.Plan = Validate(wb, snap)
		if len(res.Plan.Problems) > 0 {
			return errWorkbookInvalid
		}
		report, err := apply(ctx, tx, now, actor, fileName, res.Plan, snap)
		res.Report = report
		return err
	})
	if errors.Is(err, errWorkbookInvalid) {
		return Result{Plan: res.Plan}, err
	}
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

func apply(ctx context.Context, tx pgx.Tx, now time.Time, actor auth.Principal, fileName string, plan Plan, snap Snapshot) (*access.ImportReport, error) {
	report := &access.ImportReport{ID: uuid.Must(uuid.NewV7()), Source: "WORKBOOK", FileName: &fileName,
		ImportedBy: access.Person{ID: actor.EmployeeID, FullName: actor.FullName}, ImportedAt: now, Rejected: []access.Rejected{}}

	employees := map[string]uuid.UUID{}
	for email, e := range snap.Employees {
		employees[email] = e.ID
	}
	optional := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	for _, e := range plan.employees {
		var manager *uuid.UUID
		if e.ManagerEmail != "" {
			id := employees[e.ManagerEmail]
			manager = &id
		}
		id, err := people.ImportEmployee(ctx, tx, actor, people.NewEmployee{Email: e.Email, FullName: e.FullName,
			Department: optional(e.Department), JobTitle: optional(e.JobPosition), ManagerID: manager},
			map[string]any{"importId": report.ID, "sheet": SheetEmployees, "row": e.Row})
		if err != nil {
			return nil, fmt.Errorf("%s row %d: %w", SheetEmployees, e.Row, err)
		}
		employees[e.Email] = id
	}

	type toolRef struct {
		id     uuid.UUID
		method string
		levels map[string]uuid.UUID // lowercase name
	}
	tools := map[string]*toolRef{}
	for key, t := range snap.Tools {
		ref := &toolRef{t.ID, t.Method, map[string]uuid.UUID{}}
		for k, l := range t.Levels {
			ref.levels[k] = l.ID
		}
		tools[key] = ref
	}
	createLevel := func(ref *toolRef, l ToolRow) error {
		steps := Steps[l.Steps]
		level, err := catalog.CreateLevelTx(ctx, tx, now, actor, ref.id, catalog.NewLevel{Name: l.Level,
			RequiresToolOwner: steps[0], RequiresInfosec: steps[1]}, false)
		if err != nil {
			return fmt.Errorf("%s row %d: %w", SheetTools, l.Row, err)
		}
		ref.levels[lower(l.Level)] = level.ID
		return nil
	}
	for _, t := range plan.tools {
		tool, err := catalog.CreateToolTx(ctx, tx, now, actor, catalog.NewTool{Name: t.Tool, FulfillmentMethod: Methods[t.Method],
			OwnerID: employees[t.OwnerEmail], AdminGroupID: snap.Groups[t.AdminGroup]})
		if err != nil {
			return nil, fmt.Errorf("%s row %d: %w", SheetTools, t.Row, err)
		}
		ref := &toolRef{tool.ID, tool.FulfillmentMethod, map[string]uuid.UUID{}}
		tools[lower(t.Tool)] = ref
		for _, l := range t.levels {
			if err := createLevel(ref, l); err != nil {
				return nil, err
			}
		}
	}
	for _, l := range plan.levels {
		if err := createLevel(tools[lower(l.Tool)], l); err != nil {
			return nil, err
		}
	}

	holdings := make([]access.Holding, len(plan.access))
	for i, a := range plan.access {
		ref := tools[lower(a.Tool)]
		holdings[i] = access.Holding{Email: a.EmployeeEmail, Account: a.Account, Tool: ref.id, Level: ref.levels[lower(a.Level)], Method: ref.method}
	}
	if err := access.RecordHoldings(ctx, tx, now, actor, report, holdings); err != nil {
		return nil, err
	}

	levels := plan.Summary.LevelsNew
	report.Created = map[string]int{"employees": len(plan.employees), "tools": len(plan.tools), "levels": levels}
	if levels > 0 {
		// One notification for the whole import instead of one per level (F2, M17).
		if err := notify.ToRole(ctx, tx, "INFOSEC", "APPROVAL_POLICIES_IMPORTED", "import", report.ID,
			map[string]any{"fileName": fileName, "tools": len(plan.tools), "levels": levels}); err != nil {
			return nil, err
		}
	}
	return report, access.SaveReport(ctx, tx, actor, report)
}

func (s *Service) snapshot(ctx context.Context, lock bool) (Snapshot, error) {
	var snap Snapshot
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, _ time.Time) (err error) {
		snap, err = load(ctx, tx, lock)
		return err
	})
	return snap, err
}

// load reads what the workbook is checked against. With lock, the catalog entries, tools and
// levels and employees are held FOR SHARE until the import commits, so none can be retired or
// change status in between.
func load(ctx context.Context, tx pgx.Tx, lock bool) (Snapshot, error) {
	share := ""
	if lock {
		share = " FOR SHARE"
	}
	s := Snapshot{Departments: map[string]bool{}, Positions: map[string]bool{}, Groups: map[string]uuid.UUID{},
		Employees: map[string]Employee{}, Tools: map[string]Tool{}, Imported: map[Key]Imported{}, Held: map[[2]uuid.UUID]bool{}}
	each := func(sql string, scan func(pgx.Rows) error) error {
		rows, err := tx.Query(ctx, sql)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := scan(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	var name string
	var active bool
	var id uuid.UUID
	steps := func(rows pgx.Rows) (Level, uuid.UUID, error) {
		var l Level
		var tool uuid.UUID
		var owner, infosec bool
		var status string
		err := rows.Scan(&l.ID, &tool, &l.Name, &owner, &infosec, &status)
		l.Steps, l.Active = StepsName(owner, infosec), status == "ACTIVE"
		return l, tool, err
	}
	toolsByID := map[uuid.UUID]string{}
	err := errors.Join(
		each("SELECT name, active FROM departments"+share, func(r pgx.Rows) error {
			err := r.Scan(&name, &active)
			s.Departments[name] = active
			return err
		}),
		each("SELECT name, active FROM job_positions"+share, func(r pgx.Rows) error {
			err := r.Scan(&name, &active)
			s.Positions[name] = active
			return err
		}),
		each("SELECT name, id FROM admin_groups", func(r pgx.Rows) error {
			err := r.Scan(&name, &id)
			s.Groups[name] = id
			return err
		}),
		each(`SELECT e.id, e.email, e.full_name, coalesce(e.department, ''), coalesce(e.job_title, ''), coalesce(m.email, ''), e.status
			FROM employees e LEFT JOIN employees m ON m.id = e.manager_id`+strings.Replace(share, "SHARE", "SHARE OF e", 1), func(r pgx.Rows) error {
			var e Employee
			err := r.Scan(&e.ID, &e.Email, &e.FullName, &e.Department, &e.JobTitle, &e.ManagerEmail, &e.Status)
			s.Employees[e.Email] = e
			return err
		}),
		each(`SELECT t.id, t.name, t.fulfillment_method, o.email, g.name, t.status FROM tools t
			JOIN employees o ON o.id = t.owner_id JOIN admin_groups g ON g.id = t.admin_group_id`+strings.Replace(share, "SHARE", "SHARE OF t", 1), func(r pgx.Rows) error {
			t := Tool{Levels: map[string]Level{}}
			err := r.Scan(&t.ID, &t.Name, &t.Method, &t.OwnerEmail, &t.AdminGroup, &t.Status)
			s.Tools[lower(t.Name)], toolsByID[t.ID] = t, lower(t.Name)
			return err
		}),
	)
	if err != nil {
		return Snapshot{}, err
	}
	err = errors.Join(
		each("SELECT id, tool_id, name, requires_tool_owner, requires_infosec, status FROM access_levels"+share, func(r pgx.Rows) error {
			l, tool, err := steps(r)
			s.Tools[toolsByID[tool]].Levels[lower(l.Name)] = l
			return err
		}),
		each(`SELECT tool_id, access_level_id, lower(account), employee_id, status NOT IN ('REVOKED', 'CANCELLED') FROM grants
			WHERE import_id IS NOT NULL`, func(r pgx.Rows) error {
			var k Key
			var imp Imported
			err := r.Scan(&k.Tool, &k.Level, &k.Account, &imp.Employee, &imp.Open)
			s.Imported[k] = imp
			return err
		}),
		each(`SELECT employee_id, access_level_id FROM grants WHERE employee_id IS NOT NULL AND status NOT IN ('REVOKED', 'CANCELLED')`, func(r pgx.Rows) error {
			var k [2]uuid.UUID
			err := r.Scan(&k[0], &k[1])
			s.Held[k] = true
			return err
		}),
	)
	return s, err
}

// Register mounts the onboarding routes.
func (s *Service) Register(r chi.Router) {
	r.Get("/imports/workbook/template", s.handleTemplate)
	r.Post("/imports/workbook/preview", s.handlePreview)
	r.Post("/imports/workbook", s.handleImport)
}

func actor(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

func readFile(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUpload))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, errFileTooLarge
	}
	return data, err
}

func (s *Service) handleTemplate(w http.ResponseWriter, r *http.Request) {
	data, err := s.TemplateFile(r.Context(), actor(r))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", xlsxType)
	w.Header().Set("Content-Disposition", `attachment; filename="accessflow-onboarding-`+time.Now().UTC().Format("2006-01-02")+`.xlsx"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Service) handlePreview(w http.ResponseWriter, r *http.Request) {
	if err := canOnboard(actor(r)); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	data, err := readFile(w, r)
	var plan Plan
	if err == nil {
		plan, err = s.Preview(r.Context(), actor(r), data)
	}
	platform.Respond(w, r, http.StatusOK, plan, err)
}

func (s *Service) handleImport(w http.ResponseWriter, r *http.Request) {
	if err := canOnboard(actor(r)); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	data, err := readFile(w, r)
	var res Result
	if err == nil {
		res, err = s.Import(r.Context(), actor(r), r.URL.Query().Get("fileName"), data)
	}
	if errors.Is(err, errWorkbookInvalid) {
		platform.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "workbook_invalid", "plan": res.Plan})
		return
	}
	platform.Respond(w, r, http.StatusCreated, res, err)
}
