package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Bootstrap creates the initial Super Admin from email if no Super Admin has ever
// existed (A4, D2). The temporary password goes to the bootstrap file, never to the
// log, and does not expire; it is valid until the first password change (F4).
func (s *Service) Bootstrap(ctx context.Context, email string) error {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return fmt.Errorf("BOOTSTRAP_ADMIN_EMAIL %q is not a valid email address", email)
	}
	email = normalized
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
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('accessflow bootstrap'))"); err != nil {
			return err
		}
		var existed bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM role_assignments WHERE role = 'SUPER_ADMIN')").Scan(&existed); err != nil {
			return err
		}
		if existed {
			return nil
		}

		var id uuid.UUID
		if err := tx.QueryRow(ctx, "INSERT INTO employees (email, full_name) VALUES ($1, 'Initial Super Admin') RETURNING id",
			email).Scan(&id); err != nil {
			return fmt.Errorf("create initial Super Admin: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO password_credentials (employee_id, password_hash, is_temporary, expires_at)
			VALUES ($1, $2, true, $3)`, id, hash, TemporaryPasswordExpiry(now, true)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO role_assignments (employee_id, role, valid_from) VALUES ($1, 'SUPER_ADMIN', $2)", id, now); err != nil {
			return err
		}
		for _, e := range []audit.Event{
			{Action: "EMPLOYEE_CREATED", Entity: "employee", EntityID: &id, SubjectID: &id, Details: map[string]any{"bootstrap": true}},
			{Action: "ROLE_ASSIGNED", Entity: "employee", EntityID: &id, SubjectID: &id, Details: map[string]any{"role": "SUPER_ADMIN", "bootstrap": true}},
		} {
			if err := audit.Record(ctx, tx, e); err != nil {
				return err
			}
		}
		// Written before commit: if the commit fails the file is removed below, so an
		// admin never exists without a readable password.
		if err := writeBootstrapFile(s.bootstrapFile, "AccessFlow initial Super Admin", id, email, password); err != nil {
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
	if wrote {
		s.log.Info("initial Super Admin created; read the one-time password with: docker compose exec app /accessflow bootstrap-password",
			"file", s.bootstrapFile)
	}
	return nil
}

func writeBootstrapFile(path, title string, id uuid.UUID, email, password string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".bootstrap-*") // created with mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // no-op after the rename
	_, err = fmt.Fprintf(f, "%s\nemail: %s\nemployee_id: %s\npassword: %s\n"+
		"Sign in with this password and change it; this file is then deleted.\n", title, email, id, password)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

var employeeIDLine = regexp.MustCompile(`(?m)^employee_id: (\S+)$`)

// RemoveFinishedBootstrapFile deletes the bootstrap file once its Super Admin no longer
// has a temporary password. It runs after every password change and at startup, which
// covers a crash between the change and the deletion.
func (s *Service) RemoveFinishedBootstrapFile(ctx context.Context) error {
	data, err := os.ReadFile(s.bootstrapFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	m := employeeIDLine.FindSubmatch(data)
	if m == nil {
		return fmt.Errorf("%s: no employee_id line", s.bootstrapFile)
	}
	var temporary bool
	err = s.pool.QueryRow(ctx, "SELECT is_temporary FROM password_credentials WHERE employee_id = $1", string(m[1])).Scan(&temporary)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if temporary {
		return nil
	}
	return os.Remove(s.bootstrapFile)
}

// LoadSecret returns the JWT signing secret: JWT_SECRET when set (at least 32 bytes),
// otherwise a random secret generated on first start and kept in settings (A4).
func LoadSecret(ctx context.Context, pool *pgxpool.Pool, fromEnv string) ([]byte, error) {
	if fromEnv != "" {
		if len(fromEnv) < 32 {
			return nil, errors.New("JWT_SECRET must be at least 32 bytes")
		}
		return []byte(fromEnv), nil
	}
	generated := make([]byte, 32)
	if _, err := rand.Read(generated); err != nil {
		return nil, err
	}
	var stored string
	err := platform.InTx(ctx, pool, func(tx pgx.Tx, _ time.Time) error {
		tag, err := tx.Exec(ctx, "INSERT INTO settings (key, value) VALUES ('jwt_secret', $1) ON CONFLICT (key) DO NOTHING",
			base64.StdEncoding.EncodeToString(generated))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			if err := audit.Record(ctx, tx, audit.Event{Action: "JWT_SECRET_GENERATED", Entity: "settings"}); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx, "SELECT value FROM settings WHERE key = 'jwt_secret'").Scan(&stored)
	})
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(stored)
}
