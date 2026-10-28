package devfixture

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

const password = "accessflow-local-demo"

func setup(t *testing.T) (testutil.DB, *people.Service, *httptest.Server) {
	db := testutil.NewDB(t)
	authSvc, err := auth.NewService(db.App, auth.Options{Secret: []byte(strings.Repeat("k", 32)), BootstrapFile: t.TempDir() + "/a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Mount("/api/v1/auth", authSvc.Routes())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return db, people.NewService(db.App, authSvc), srv
}

func count(t *testing.T, db testutil.DB, sql string) int {
	var n int
	if err := db.App.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDevFixtureIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, svc, srv := setup(t)

	created, err := Run(ctx, svc, password)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 7 {
		t.Fatalf("created %v; want 7 employees", created)
	}
	if n := count(t, db, "SELECT count(*) FROM employees WHERE seeded"); n != 7 {
		t.Fatalf("seeded employees = %d; want 7", n)
	}
	if n := count(t, db, "SELECT count(*) FROM role_assignments WHERE role = 'INFOSEC' AND valid_to IS NULL"); n != 2 {
		t.Fatalf("InfoSec members = %d; want 2 (F2 distinct-person fallback)", n)
	}
	if n := count(t, db, `SELECT count(*) FROM employees e JOIN employees m ON m.id = e.manager_id
		WHERE m.email = 'manager@accessflow.local'`); n != 2 {
		t.Fatalf("reports of manager@ = %d; want 2", n)
	}
	if n := count(t, db, "SELECT count(*) FROM audit_events WHERE actor_id IS NOT NULL"); n != 0 {
		t.Fatalf("fixture events with an actor = %d; want 0 (the system acted)", n)
	}
	if n := count(t, db, "SELECT count(*) FROM audit_events WHERE action = 'EMPLOYEE_CREATED' AND details->>'seed' = 'true'"); n != 7 {
		t.Fatalf("EMPLOYEE_CREATED marked seed = %d; want 7", n)
	}
	for _, email := range created {
		r := testutil.NewClient(t, srv).Login(email, password)
		testutil.RequireStatus(t, r, http.StatusOK, "")
		if r.Body["mustChangePassword"] != false {
			t.Fatalf("%s must change the password; want the shared seed password to be permanent", email)
		}
	}

	events := count(t, db, "SELECT count(*) FROM audit_events WHERE action NOT LIKE 'LOGIN%'")
	again, err := Run(ctx, svc, password)
	if err != nil || len(again) != 0 {
		t.Fatalf("second run created %v, %v; want nothing", again, err)
	}
	if n := count(t, db, "SELECT count(*) FROM audit_events WHERE action NOT LIKE 'LOGIN%'"); n != events {
		t.Fatalf("second run added %d audit events; want 0", n-events)
	}
}

func TestDevFixtureRejectsAShortPassword(t *testing.T) {
	db, svc, _ := setup(t)
	if _, err := Run(context.Background(), svc, "short"); err == nil {
		t.Fatal("short password accepted")
	}
	if n := count(t, db, "SELECT count(*) FROM employees"); n != 0 {
		t.Fatalf("employees = %d; want 0", n)
	}
}

// Re-running does not undo an administrator's change to an existing fixture employee.
func TestDevFixtureLeavesExistingEmployeesAlone(t *testing.T) {
	ctx := context.Background()
	db, svc, _ := setup(t)
	if _, err := Run(ctx, svc, password); err != nil {
		t.Fatal(err)
	}
	if _, err := db.App.Exec(ctx, `UPDATE role_assignments SET valid_to = now()
		WHERE employee_id = (SELECT id FROM employees WHERE email = 'auditor@accessflow.local')`); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, svc, password); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM role_assignments r JOIN employees e ON e.id = r.employee_id
		WHERE e.email = 'auditor@accessflow.local' AND r.valid_to IS NULL`); n != 0 {
		t.Fatalf("open roles of auditor@ after re-run = %d; want 0 (the removal stands)", n)
	}
}
