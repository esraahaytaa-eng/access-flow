package people

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// testEnv serves the real auth and people routes, plus probe routes standing in for
// later role-guarded endpoints.
type testEnv struct {
	t   *testing.T
	db  testutil.DB
	svc *Service
	srv *httptest.Server
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	db := testutil.NewDB(t)
	authSvc, err := auth.NewService(db.App, auth.Options{Secret: []byte(strings.Repeat("k", 32)), BootstrapFile: t.TempDir() + "/admin.txt"})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(db.App, authSvc)
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Mount("/auth", authSvc.Routes())
		r.Group(func(r chi.Router) {
			r.Use(authSvc.RequireSession)
			svc.Register(r)
			r.With(auth.RequireRole("INFOSEC")).Get("/probe/infosec", ok)
		})
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &testEnv{t: t, db: db, svc: svc, srv: srv}
}

// user creates an active employee with the test password and roles, signed in.
func (e *testEnv) user(roles ...string) (testutil.Employee, *testutil.Client) {
	e.t.Helper()
	emp := testutil.CreateEmployee(e.t, e.db, testutil.EmployeeSpec{Roles: roles, Password: true})
	c := testutil.NewClient(e.t, e.srv)
	testutil.RequireStatus(e.t, c.Login(emp.Email, testutil.Password), http.StatusOK, "")
	return emp, c
}

func (e *testEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.db.App.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *testEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.App.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (e *testEnv) audits(action string, subject uuid.UUID) int {
	return e.count("SELECT count(*) FROM audit_events WHERE action = $1 AND subject_employee_id = $2", action, subject)
}

// whileLocked holds the employee's row lock in a second transaction, runs sql there, starts
// call, waits until call is blocked on that lock, then commits and returns call's error.
// It makes the "change committed while I waited for the lock" race deterministic.
func (e *testEnv) whileLocked(id uuid.UUID, sql string, args []any, call func() error) error {
	e.t.Helper()
	ctx := context.Background()
	tx, err := e.db.App.Begin(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", id); err != nil {
		e.t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	done := make(chan error, 1)
	go func() { done <- call() }()
	for deadline := time.Now().Add(5 * time.Second); e.count("SELECT count(*) FROM pg_locks WHERE NOT granted") == 0; {
		if time.Now().After(deadline) {
			e.t.Fatal("call never waited for the row lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		e.t.Fatal(err)
	}
	return <-done
}
