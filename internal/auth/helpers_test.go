package auth

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

const goodPassword = "correct horse battery"

var testMailKey = bytes.Repeat([]byte{1}, 32)

// testEnv is a real database plus an HTTP server mounting the auth routes and three
// probe routes standing in for later business endpoints.
type testEnv struct {
	t    *testing.T
	db   testutil.DB
	svc  *Service
	srv  *httptest.Server
	logs *bytes.Buffer
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	db := testutil.NewDB(t)
	logs := &bytes.Buffer{}
	svc, err := NewService(db.App, Options{
		Secret:          testSecret,
		CompanyTimezone: "Europe/Istanbul",
		BootstrapFile:   filepath.Join(t.TempDir(), "bootstrap-admin.txt"),
		Logger:          slog.New(slog.NewTextHandler(logs, nil)),
		LoginRateLimit:  1000, // tests sign in many times from one address; ratelimit_test.go tests the default
		MailKey:         testMailKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	ok := func(w http.ResponseWriter, _ *http.Request) {
		platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	r := chi.NewRouter()
	r.Mount("/api/v1/auth", svc.Routes())
	r.With(svc.RequireSession).Get("/api/v1/probe", ok)
	r.With(svc.RequireSession).Post("/api/v1/probe", ok)
	r.With(svc.RequireSession, RequireRole("SUPER_ADMIN")).Get("/api/v1/probe/admin", ok)
	r.With(svc.RequireSession).Route("/api/v1/", svc.RegisterActivations)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &testEnv{t: t, db: db, svc: svc, srv: srv, logs: logs}
}

type employee struct {
	ID       uuid.UUID
	Email    string
	Password string
}

// employee creates an active employee with a permanent password.
func (e *testEnv) employee(roles ...string) employee {
	e.t.Helper()
	emp := testutil.CreateEmployee(e.t, e.db, testutil.EmployeeSpec{Roles: roles})
	e.setPassword(emp.ID, goodPassword, false, nil)
	return employee{ID: emp.ID, Email: emp.Email, Password: goodPassword}
}

func (e *testEnv) setPassword(id uuid.UUID, pw string, temporary bool, expiresAt *time.Time) {
	e.t.Helper()
	hash, err := HashPassword(pw)
	if err != nil {
		e.t.Fatal(err)
	}
	_, err = e.db.App.Exec(context.Background(), `INSERT INTO password_credentials (employee_id, password_hash, is_temporary, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (employee_id) DO UPDATE SET password_hash = $2, is_temporary = $3, expires_at = $4`,
		id, hash, temporary, expiresAt)
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.db.App.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *testEnv) auditCount(action string, subject uuid.UUID) int {
	e.t.Helper()
	var n int
	err := e.db.App.QueryRow(context.Background(),
		"SELECT count(*) FROM audit_events WHERE action = $1 AND subject_employee_id IS NOT DISTINCT FROM $2",
		action, nullable(subject)).Scan(&n)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func nullable(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func (e *testEnv) client() *testutil.Client { return testutil.NewClient(e.t, e.srv) }
