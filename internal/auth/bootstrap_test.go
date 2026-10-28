package auth

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var passwordLine = regexp.MustCompile(`(?m)^password: (\S+)$`)

func bootstrapPassword(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read bootstrap file: %v", err)
	}
	m := passwordLine.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatalf("no password line in %q", data)
	}
	return m[1]
}

func TestBootstrapCreatesTheInitialSuperAdminOnce(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	file := env.svc.bootstrapFile

	if err := env.svc.Bootstrap(ctx, "Admin@AccessFlow.local"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("bootstrap file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v; want 0600", info.Mode().Perm())
	}
	password := bootstrapPassword(t, file)
	if strings.Contains(env.logs.String(), password) {
		t.Fatal("the bootstrap password appears in the log")
	}
	if !strings.Contains(env.logs.String(), "docker compose exec app /accessflow bootstrap-password") {
		t.Errorf("log %q does not say how to read the file", env.logs.String())
	}

	var role, email string
	var temporary, noExpiry bool
	err = env.db.App.QueryRow(ctx, `SELECT r.role, e.email, c.is_temporary, c.expires_at IS NULL
		FROM employees e JOIN role_assignments r ON r.employee_id = e.id JOIN password_credentials c ON c.employee_id = e.id`).
		Scan(&role, &email, &temporary, &noExpiry)
	if err != nil {
		t.Fatal(err)
	}
	if role != "SUPER_ADMIN" || email != "admin@accessflow.local" || !temporary || !noExpiry {
		t.Fatalf("got role %s email %s temporary %v no-expiry %v; want a Super Admin with a non-expiring temporary password",
			role, email, temporary, noExpiry)
	}

	// A restart does nothing: a Super Admin has existed.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.Bootstrap(ctx, "admin@accessflow.local"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("bootstrap ran twice (file: %v)", err)
	}
	var admins int
	_ = env.db.App.QueryRow(ctx, "SELECT count(*) FROM role_assignments WHERE role = 'SUPER_ADMIN'").Scan(&admins)
	if admins != 1 {
		t.Fatalf("Super Admin assignments = %d; want 1", admins)
	}
}

// Even after the role is removed, "a Super Admin has ever existed" stays true (A4).
func TestBootstrapDoesNotRunAgainAfterTheRoleWasRemoved(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	admin := env.employee("SUPER_ADMIN")
	env.exec("UPDATE role_assignments SET valid_to = now() WHERE employee_id = $1", admin.ID)

	if err := env.svc.Bootstrap(ctx, "admin@accessflow.local"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(env.svc.bootstrapFile); !os.IsNotExist(err) {
		t.Fatalf("bootstrap created a new Super Admin (file: %v)", err)
	}
}

func TestBootstrapAdminMustChangeThePasswordAndTheFileIsDeleted(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	if err := env.svc.Bootstrap(ctx, "admin@accessflow.local"); err != nil {
		t.Fatal(err)
	}
	password := bootstrapPassword(t, env.svc.bootstrapFile)

	c := env.client()
	r := c.login("admin@accessflow.local", password)
	requireStatus(t, r, http.StatusOK, "")
	if r.body["mustChangePassword"] != true {
		t.Fatalf("mustChangePassword = %v; want true", r.body["mustChangePassword"])
	}
	requireStatus(t, c.do(http.MethodGet, "/api/v1/probe/admin", nil), http.StatusForbidden, "password_change_required")

	requireStatus(t, c.do(http.MethodPost, "/api/v1/auth/password",
		map[string]string{"currentPassword": password, "newPassword": "the admin's own password"}), http.StatusOK, "")
	if _, err := os.Stat(env.svc.bootstrapFile); !os.IsNotExist(err) {
		t.Fatalf("bootstrap file still exists after the change (%v)", err)
	}
	requireStatus(t, c.do(http.MethodGet, "/api/v1/probe/admin", nil), http.StatusOK, "")
	requireStatus(t, env.client().login("admin@accessflow.local", password), http.StatusUnauthorized, "invalid_credentials")
}

func TestStaleBootstrapFileIsRemovedAtStartup(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	if err := env.svc.Bootstrap(ctx, "admin@accessflow.local"); err != nil {
		t.Fatal(err)
	}
	// The password was changed, but the process stopped before deleting the file.
	env.exec("UPDATE password_credentials SET is_temporary = false")

	if err := env.svc.RemoveFinishedBootstrapFile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(env.svc.bootstrapFile); !os.IsNotExist(err) {
		t.Fatalf("stale bootstrap file kept (%v)", err)
	}
	if err := env.svc.RemoveFinishedBootstrapFile(ctx); err != nil {
		t.Fatalf("no file: %v; want nil", err)
	}
}

func TestBootstrapCreatesNothingWhenTheFileCannotBeWritten(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env.svc.bootstrapFile = filepath.Join(blocker, "bootstrap-admin.txt")

	if err := env.svc.Bootstrap(ctx, "admin@accessflow.local"); err == nil {
		t.Fatal("bootstrap succeeded; want an error")
	}
	var employees int
	_ = env.db.App.QueryRow(ctx, "SELECT count(*) FROM employees").Scan(&employees)
	if employees != 0 {
		t.Fatalf("employees = %d; want 0 (an admin whose password nobody can read)", employees)
	}
}

func TestLoadSecret(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)

	if _, err := LoadSecret(ctx, env.db.App, "too-short"); err == nil {
		t.Error("short JWT_SECRET accepted; want at least 32 bytes")
	}
	fromEnv, err := LoadSecret(ctx, env.db.App, strings.Repeat("s", 32))
	if err != nil || string(fromEnv) != strings.Repeat("s", 32) {
		t.Errorf("JWT_SECRET: got %q, %v", fromEnv, err)
	}

	first, err := LoadSecret(ctx, env.db.App, "")
	if err != nil || len(first) != 32 {
		t.Fatalf("generated secret: %d bytes, %v; want 32", len(first), err)
	}
	second, _ := LoadSecret(ctx, env.db.App, "")
	if string(first) != string(second) {
		t.Fatal("secret changed between starts; want it stored once and reused")
	}
	var events int
	_ = env.db.App.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action = 'JWT_SECRET_GENERATED'").Scan(&events)
	if events != 1 {
		t.Fatalf("JWT_SECRET_GENERATED events = %d; want 1", events)
	}
}
