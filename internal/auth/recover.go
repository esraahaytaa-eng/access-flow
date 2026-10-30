package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var (
	ErrSuperAdminActive = errors.New("an active Super Admin exists: promote through a Super Admin nomination instead")
	ErrNotActive        = errors.New("the employee is not active")
)

// LifecycleLockKey serializes every person-level change (people.LockLifecycle) and recovery,
// so "no active Super Admin" is never decided on stale data. Declared here because people imports auth.
const LifecycleLockKey int64 = 0x0D7_11FE

// RecoverAdmin is the last-resort recovery, run from the server's command line (D2): only
// while no active Super Admin exists, it makes the employee with email (created when there
// is none) a Super Admin with a one-time password in the bootstrap file, like the first start.
func (s *Service) RecoverAdmin(ctx context.Context, email string) error {
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	password, err := NewTemporaryPassword()
	if err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	wrote := false
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", LifecycleLockKey); err != nil {
			return err
		}
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM role_assignments r JOIN employees e ON e.id = r.employee_id
			WHERE r.role = 'SUPER_ADMIN' AND r.valid_to IS NULL AND e.status = 'ACTIVE')`).Scan(&active); err != nil {
			return err
		}
		if active {
			return ErrSuperAdminActive
		}
		var id uuid.UUID
		var status string
		err := tx.QueryRow(ctx, "SELECT id, status FROM employees WHERE email = $1 FOR UPDATE", email).Scan(&id, &status)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if err := tx.QueryRow(ctx, "INSERT INTO employees (email, full_name) VALUES ($1, 'Recovered Super Admin') RETURNING id",
				email).Scan(&id); err != nil {
				return err
			}
			if err := audit.Record(ctx, tx, audit.Event{Action: "EMPLOYEE_CREATED", Entity: "employee", EntityID: &id, SubjectID: &id,
				Details: map[string]any{"recovery": true}}); err != nil {
				return err
			}
		case err != nil:
			return err
		case status != "ACTIVE":
			return ErrNotActive
		}
		// Every earlier session ends; the one-time password replaces theirs (A4, F4).
		if _, err := tx.Exec(ctx, "UPDATE employees SET token_version = token_version + 1, version = version + 1 WHERE id = $1", id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO password_credentials (employee_id, password_hash, is_temporary)
			VALUES ($1, $2, true) ON CONFLICT (employee_id) DO UPDATE
			SET password_hash = EXCLUDED.password_hash, is_temporary = true, expires_at = NULL, updated_at = now()`,
			id, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO role_assignments (employee_id, role, valid_from) VALUES ($1, 'SUPER_ADMIN', $2)", id, now); err != nil {
			return err
		}
		for _, e := range []audit.Event{
			{Action: "ROLE_ASSIGNED", Entity: "employee", EntityID: &id, SubjectID: &id, Details: map[string]any{"role": "SUPER_ADMIN", "recovery": true}},
			{Action: "ADMIN_RECOVERED", Entity: "employee", EntityID: &id, SubjectID: &id},
		} {
			if err := audit.Record(ctx, tx, e); err != nil {
				return err
			}
		}
		if err := writeBootstrapFile(s.bootstrapFile, "AccessFlow recovered Super Admin", id, email, password); err != nil {
			return fmt.Errorf("write %s: %w", s.bootstrapFile, err)
		}
		wrote = true
		return nil
	})
	if err != nil {
		if wrote {
			os.Remove(s.bootstrapFile)
		}
		return err
	}
	s.log.Info("Super Admin recovered; read the one-time password with: docker compose exec app /accessflow bootstrap-password",
		"email", email, "file", s.bootstrapFile)
	return nil
}
