package people

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var (
	errEmailTaken       = platform.NewError(http.StatusConflict, "email_taken")
	errManagerNotFound  = platform.ValidationError("managerId", "manager_not_found")
	errManagerInactive  = platform.ValidationError("managerId", "manager_inactive")
	errManagerCycle     = platform.ValidationError("managerId", "manager_cycle")
	errTerminated       = platform.ValidationError("", "employee_terminated")
	errVersionConflict  = platform.NewError(http.StatusConflict, "version_conflict")
	errTransferRequired = platform.NewError(http.StatusConflict, "transfer_required")
	errNotActive        = platform.ValidationError("", "employee_not_active")
)

// Service runs the people transactions.
type Service struct {
	pool *pgxpool.Pool
	auth *auth.Service
}

func NewService(pool *pgxpool.Pool, authSvc *auth.Service) *Service {
	return &Service{pool: pool, auth: authSvc}
}

// SystemActor is the system acting with administrative rights; only the dev fixture uses
// it. Its nil ID is audited as the system (no actor).
var SystemActor = auth.Principal{Roles: []string{"HR_MANAGER", "SUPER_ADMIN"}}

func actorRef(p auth.Principal) *uuid.UUID {
	if p.EmployeeID == uuid.Nil {
		return nil
	}
	return &p.EmployeeID
}

// Employee is the full view of an employee.
type Employee struct {
	ID                 uuid.UUID    `json:"id"`
	Email              string       `json:"email"`
	FullName           string       `json:"fullName"`
	Department         *string      `json:"department"`
	JobTitle           *string      `json:"jobTitle"`
	ManagerID          *uuid.UUID   `json:"managerId"`
	Status             string       `json:"status"`
	Roles              []string     `json:"roles"`
	AdminGroups        []auth.Group `json:"adminGroups"`
	MustChangePassword bool         `json:"mustChangePassword"`
	Termination        *Termination `json:"termination"`
	Version            int          `json:"version"`
	CreatedAt          time.Time    `json:"createdAt"`
}

// Termination is a scheduled termination (D7).
type Termination struct {
	At          time.Time `json:"at"`
	Reason      string    `json:"reason"`
	ScheduledBy uuid.UUID `json:"scheduledBy"`
}

// basicEmployee is what employees without a directory role see (approved 2026-10-28).
type basicEmployee struct {
	ID         uuid.UUID  `json:"id"`
	Email      string     `json:"email"`
	FullName   string     `json:"fullName"`
	Department *string    `json:"department"`
	JobTitle   *string    `json:"jobTitle"`
	ManagerID  *uuid.UUID `json:"managerId"`
}

const selectEmployees = `
	SELECT e.id, e.email, e.full_name, e.department, e.job_title, e.manager_id, e.status, e.version, e.created_at,
	       COALESCE(c.is_temporary, false),
	       ARRAY(SELECT r.role FROM role_assignments r WHERE r.employee_id = e.id AND r.valid_to IS NULL ORDER BY r.role),
	       ` + auth.OpenGroupsJSON + `,
	       CASE WHEN e.termination_at IS NOT NULL THEN json_build_object('at', e.termination_at, 'reason', e.termination_reason,
	           'scheduledBy', e.termination_scheduled_by) END
	FROM employees e LEFT JOIN password_credentials c ON c.employee_id = e.id`

func scanEmployee(row pgx.Row) (Employee, error) {
	var e Employee
	err := row.Scan(&e.ID, &e.Email, &e.FullName, &e.Department, &e.JobTitle, &e.ManagerID, &e.Status, &e.Version,
		&e.CreatedAt, &e.MustChangePassword, &e.Roles, &e.AdminGroups, &e.Termination)
	return e, err
}

func GetEmployee(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Employee, error) {
	e, err := scanEmployee(tx.QueryRow(ctx, selectEmployees+" WHERE e.id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Employee{}, platform.ErrNotFound
	}
	return e, err
}

// NewEmployee is the input for CreateEmployee.
type NewEmployee struct {
	Email, FullName      string
	Department, JobTitle *string
	ManagerID            *uuid.UUID
	// SeedPassword gives a seeded employee this permanent password instead of a temporary
	// one. Set only by the dev fixture; never read from HTTP input.
	SeedPassword string
}

// CreateEmployee creates an active employee. Unless SeedPassword is set it returns a
// temporary password (72 hours, must be changed), shown to the caller once (F4).
func (s *Service) CreateEmployee(ctx context.Context, actor auth.Principal, in NewEmployee) (Employee, string, error) {
	if !canManageEmployees(actor) {
		return Employee{}, "", platform.ErrForbidden
	}
	email, err := auth.NormalizeEmail(in.Email)
	if err != nil {
		return Employee{}, "", err
	}
	name, err := cleanName(in.FullName)
	if err != nil {
		return Employee{}, "", err
	}
	department, err := CleanText(in.Department, "department")
	if err != nil {
		return Employee{}, "", err
	}
	jobTitle, err := CleanText(in.JobTitle, "jobTitle")
	if err != nil {
		return Employee{}, "", err
	}
	password, temporary := in.SeedPassword, false
	if password != "" {
		if err := auth.ValidatePassword(password); err != nil {
			return Employee{}, "", err
		}
	} else if password, err = auth.NewTemporaryPassword(); err != nil {
		return Employee{}, "", err
	} else {
		temporary = true
	}
	hash, err := s.auth.Hash(ctx, password)
	if err != nil {
		return Employee{}, "", err
	}

	var created Employee
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		if in.ManagerID != nil {
			if err := checkManager(ctx, tx, *in.ManagerID); err != nil {
				return err
			}
		}
		var id uuid.UUID
		seeded := in.SeedPassword != ""
		err := tx.QueryRow(ctx, `INSERT INTO employees (email, full_name, department, job_title, manager_id, seeded)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, email, name, department, jobTitle, in.ManagerID, seeded).Scan(&id)
		if isUniqueViolation(err) {
			return errEmailTaken
		}
		if err != nil {
			return err
		}
		var expires *time.Time
		if temporary {
			expires = auth.TemporaryPasswordExpiry(now, false)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO password_credentials (employee_id, password_hash, is_temporary, expires_at)
			VALUES ($1, $2, $3, $4)`, id, hash, temporary, expires); err != nil {
			return err
		}
		details := map[string]any{"email": email, "fullName": name, "department": department, "jobTitle": jobTitle, "managerId": in.ManagerID}
		if seeded {
			details["seed"] = true
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "EMPLOYEE_CREATED", Entity: "employee",
			EntityID: &id, SubjectID: &id, Details: details}); err != nil {
			return err
		}
		created, err = GetEmployee(ctx, tx, id)
		return err
	})
	if err != nil {
		return Employee{}, "", err
	}
	if !temporary {
		password = ""
	}
	return created, password, nil
}

// checkManager requires an existing, active manager.
func checkManager(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var status string
	err := tx.QueryRow(ctx, "SELECT status FROM employees WHERE id = $1 FOR SHARE", id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return errManagerNotFound
	}
	if err != nil {
		return err
	}
	if status != "ACTIVE" {
		return errManagerInactive
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ListEmployees returns every employee for the directory roles, otherwise only active
// ones; the handler also reduces those to the basic view.
func (s *Service) ListEmployees(ctx context.Context, actor auth.Principal) ([]Employee, error) {
	query := selectEmployees
	if !seesFullDirectory(actor) {
		query += " WHERE e.status = 'ACTIVE'"
	}
	// shortcut: no pagination or search; fine for ~400 employees, add both if the directory grows.
	rows, err := s.pool.Query(ctx, query+" ORDER BY e.full_name, e.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Employee{}
	for rows.Next() {
		e, err := scanEmployee(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// EmployeeUpdate replaces the editable fields; nil clears one.
type EmployeeUpdate struct {
	Version              int
	ManagerID            *uuid.UUID
	Department, JobTitle *string
}

// UpdateEmployee changes the job title. A new manager or department is a transfer (D5),
// which reviews the employee's access, so it is refused here.
func (s *Service) UpdateEmployee(ctx context.Context, actor auth.Principal, id uuid.UUID, in EmployeeUpdate) (Employee, error) {
	if !canManageEmployees(actor) {
		return Employee{}, platform.ErrForbidden
	}
	if actor.EmployeeID == id { // nobody chooses who approves their own requests (2026-10-28)
		return Employee{}, errSelfChange
	}
	department, err := CleanText(in.Department, "department")
	if err != nil {
		return Employee{}, err
	}
	jobTitle, err := CleanText(in.JobTitle, "jobTitle")
	if err != nil {
		return Employee{}, err
	}
	var updated Employee
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, _ time.Time) error {
		cur, err := LockEmployee(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Status == "TERMINATED" {
			return errTerminated
		}
		if cur.Version != in.Version {
			return errVersionConflict
		}
		if !SameID(cur.ManagerID, in.ManagerID) || !SameText(cur.Department, department) {
			return errTransferRequired // a transfer reviews the employee's access (D5)
		}
		if SameText(cur.JobTitle, jobTitle) {
			updated = cur
			return nil
		}
		if err := SetPlacement(ctx, tx, id, cur.ManagerID, cur.Department, jobTitle); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "EMPLOYEE_UPDATED", Entity: "employee",
			EntityID: &id, SubjectID: &id, Details: map[string]any{"jobTitle": map[string]any{"from": cur.JobTitle, "to": jobTitle}}}); err != nil {
			return err
		}
		updated, err = GetEmployee(ctx, tx, id)
		return err
	})
	return updated, err
}

// SetPlacement writes an employee's manager, department and job title and bumps their version.
// A caller that changes the manager must hold LifecycleLockKey: it serializes the cycle check.
func SetPlacement(ctx context.Context, tx pgx.Tx, id uuid.UUID, manager *uuid.UUID, department, jobTitle *string) error {
	_, err := tx.Exec(ctx, `UPDATE employees SET manager_id = $2, department = $3, job_title = $4, version = version + 1
		WHERE id = $1`, id, manager, department, jobTitle)
	return err
}

// CheckNewManager requires an active manager who is not the employee or one of their reports.
func CheckNewManager(ctx context.Context, tx pgx.Tx, id, manager uuid.UUID) error {
	if manager == id {
		return errManagerCycle
	}
	if err := checkManager(ctx, tx, manager); err != nil {
		return err
	}
	var cycle bool
	err := tx.QueryRow(ctx, `
		WITH RECURSIVE chain (id) AS (
			SELECT $1::uuid
			UNION -- not UNION ALL: stops even on a cycle already in the data
			SELECT e.manager_id FROM employees e JOIN chain ON e.id = chain.id WHERE e.manager_id IS NOT NULL
		)
		SELECT EXISTS (SELECT 1 FROM chain WHERE id = $2)`, manager, id).Scan(&cycle)
	if err != nil {
		return err
	}
	if cycle {
		return errManagerCycle
	}
	return nil
}

func SameID(a, b *uuid.UUID) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func SameText(a, b *string) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

// ResetPassword gives an active employee a new temporary password (72 hours), ends their
// sessions and clears a lockout (F4, A4). The password is returned once, never stored in clear.
func (s *Service) ResetPassword(ctx context.Context, actor auth.Principal, id uuid.UUID) (string, error) {
	if !canManageEmployees(actor) {
		return "", platform.ErrForbidden
	}
	password, err := auth.NewTemporaryPassword()
	if err != nil {
		return "", err
	}
	hash, err := s.auth.Hash(ctx, password)
	if err != nil {
		return "", err
	}
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		target, err := LockEmployee(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := CheckAccountAction(actor, id, target.Roles); err != nil {
			return err
		}
		if target.Status != "ACTIVE" {
			return errNotActive
		}
		if err := issueTemporaryPassword(ctx, tx, now, id, hash); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "PASSWORD_RESET", Entity: "employee",
			EntityID: &id, SubjectID: &id})
	})
	if err != nil {
		return "", err
	}
	return password, nil
}

// issueTemporaryPassword sets a 72-hour temporary password, ends every session and clears a
// lockout (A4).
func issueTemporaryPassword(ctx context.Context, tx pgx.Tx, now time.Time, id uuid.UUID, hash string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO password_credentials (employee_id, password_hash, is_temporary, expires_at, updated_at)
		VALUES ($1, $2, true, $3, $4)
		ON CONFLICT (employee_id) DO UPDATE SET password_hash = $2, is_temporary = true, expires_at = $3, updated_at = $4`,
		id, hash, auth.TemporaryPasswordExpiry(now, false), now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "UPDATE employees SET token_version = token_version + 1, failed_login_count = 0, locked_until = NULL WHERE id = $1", id)
	return err
}

// LockEmployee loads an employee under a row lock (A9: one change to a person at a time).
func LockEmployee(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Employee, error) {
	// Lock first, then read in a new statement: a statement that waited for the lock re-reads
	// only the locked row, while its role and group subqueries keep the stale snapshot.
	if err := tx.QueryRow(ctx, "SELECT id FROM employees WHERE id = $1 FOR UPDATE", id).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Employee{}, platform.ErrNotFound
		}
		return Employee{}, err
	}
	return GetEmployee(ctx, tx, id)
}

// AssignRole opens a role interval for an active employee; holding it already is a no-op (D2, A9).
func (s *Service) AssignRole(ctx context.Context, actor auth.Principal, id uuid.UUID, role string) error {
	if err := checkRoleChange(actor, id, role); err != nil {
		return err
	}
	return platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		target, err := LockEmployee(ctx, tx, id)
		if err != nil {
			return err
		}
		if target.Status != "ACTIVE" {
			return errNotActive
		}
		if slices.Contains(target.Roles, role) {
			return nil
		}
		if _, err := tx.Exec(ctx, "INSERT INTO role_assignments (employee_id, role, valid_from, granted_by) VALUES ($1, $2, $3, $4)",
			id, role, now, actorRef(actor)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "ROLE_ASSIGNED", Entity: "employee",
			EntityID: &id, SubjectID: &id, Details: map[string]any{"role": role}})
	})
}

// RemoveRole closes the open role interval (the row is kept); not holding it is a no-op.
// Allowed for any status: removing access is always possible.
func (s *Service) RemoveRole(ctx context.Context, actor auth.Principal, id uuid.UUID, role string) error {
	if err := checkRoleChange(actor, id, role); err != nil {
		return err
	}
	return platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		if _, err := LockEmployee(ctx, tx, id); err != nil {
			return err
		}
		// GREATEST: a row opened by a concurrent assign may start after this transaction did.
		tag, err := tx.Exec(ctx, `UPDATE role_assignments SET valid_to = GREATEST(valid_from, $3), revoked_by = $4
			WHERE employee_id = $1 AND role = $2 AND valid_to IS NULL`, id, role, now, actorRef(actor))
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "ROLE_REMOVED", Entity: "employee",
			EntityID: &id, SubjectID: &id, Details: map[string]any{"role": role}})
	})
}
