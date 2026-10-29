package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestRecoverAdminOnlyWithoutAnActiveSuperAdmin(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	admin := env.employee("SUPER_ADMIN")
	target := env.employee()
	file := env.svc.bootstrapFile

	if err := env.svc.RecoverAdmin(ctx, target.Email); !errors.Is(err, ErrSuperAdminActive) {
		t.Fatalf("with an active Super Admin: %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("a refused recovery wrote the file")
	}

	// The last Super Admin left (D2): recovery is now the last resort.
	env.exec("UPDATE employees SET status = 'TERMINATED' WHERE id = $1", admin.ID)
	env.exec("UPDATE role_assignments SET valid_to = now() WHERE employee_id = $1", admin.ID)
	if err := env.svc.RecoverAdmin(ctx, admin.Email); !errors.Is(err, ErrNotActive) {
		t.Fatalf("a terminated employee: %v", err)
	}
	if err := env.svc.RecoverAdmin(ctx, " "+target.Email+" "); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if env.count(`SELECT count(*) FROM role_assignments WHERE employee_id = $1 AND role = 'SUPER_ADMIN' AND valid_to IS NULL
		AND granted_by IS NULL`, target.ID) != 1 {
		t.Fatal("no Super Admin role")
	}
	if env.count("SELECT count(*) FROM audit_events WHERE action = 'ADMIN_RECOVERED' AND subject_employee_id = $1 AND actor_id IS NULL", target.ID) != 1 {
		t.Fatal("recovery not audited")
	}
	// The one-time password replaces theirs and only allows a password change (F4, A4).
	password := bootstrapPassword(t, file)
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("password file: %v %v; want mode 0600", info, err)
	}
	// The password is never logged (A4); the log only says how to read it.
	if logs := env.logs.String(); strings.Contains(logs, password) || !strings.Contains(logs, "bootstrap-password") {
		t.Fatalf("logs = %q", logs)
	}
	c := env.client()
	if r := c.Do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": target.Email, "password": target.Password}); r.Status != http.StatusUnauthorized {
		t.Fatalf("old password: %d", r.Status)
	}
	if r := c.Do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": target.Email, "password": password}); r.Status != http.StatusOK ||
		r.Body["mustChangePassword"] != true {
		t.Fatalf("recovery password: %d %v", r.Status, r.Body)
	}
	if err := env.svc.RecoverAdmin(ctx, "someone.else@example.com"); !errors.Is(err, ErrSuperAdminActive) {
		t.Fatalf("second recovery: %v", err)
	}

	// With no employee for the email, recovery creates one, like the first start.
	env.exec("UPDATE role_assignments SET valid_to = now() WHERE employee_id = $1", target.ID)
	if err := env.svc.RecoverAdmin(ctx, "New.Admin@example.com"); err != nil {
		t.Fatalf("recover a new employee: %v", err)
	}
	if env.count(`SELECT count(*) FROM employees e JOIN role_assignments r ON r.employee_id = e.id
		WHERE e.email = 'new.admin@example.com' AND r.role = 'SUPER_ADMIN' AND r.valid_to IS NULL`) != 1 {
		t.Fatal("no new Super Admin")
	}
}

func (e *testEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.App.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}
