package devfixture

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/catalog"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

const password = "accessflow-local-demo"

// fx runs the fixture on the services under test.
type fx struct {
	people  *people.Service
	catalog *catalog.Service
}

func (f fx) run(password string) ([]string, error) {
	return Run(context.Background(), f.people, f.catalog, password)
}

func setup(t *testing.T) (testutil.DB, fx, *httptest.Server) {
	db := testutil.NewDB(t)
	authSvc, err := auth.NewService(db.App, auth.Options{Secret: []byte(strings.Repeat("k", 32)), BootstrapFile: t.TempDir() + "/a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Mount("/api/v1/auth", authSvc.Routes())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return db, fx{people.NewService(db.App, authSvc), catalog.NewService(db.App)}, srv
}

func count(t *testing.T, db testutil.DB, sql string) int {
	var n int
	if err := db.App.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDevFixtureIsIdempotent(t *testing.T) {
	db, svc, srv := setup(t)

	created, err := svc.run(password)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 7+2+4 {
		t.Fatalf("created %v; want 7 employees, 2 admin groups and 4 tools", created)
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
	for _, p := range fixture {
		r := testutil.NewClient(t, srv).Login(p.email, password)
		testutil.RequireStatus(t, r, http.StatusOK, "")
		if r.Body["mustChangePassword"] != false {
			t.Fatalf("%s must change the password; want the shared seed password to be permanent", p.email)
		}
	}

	events := count(t, db, "SELECT count(*) FROM audit_events WHERE action NOT LIKE 'LOGIN%'")
	again, err := svc.run(password)
	if err != nil || len(again) != 0 {
		t.Fatalf("second run created %v, %v; want nothing", again, err)
	}
	if n := count(t, db, "SELECT count(*) FROM audit_events WHERE action NOT LIKE 'LOGIN%'"); n != events {
		t.Fatalf("second run added %d audit events; want 0", n-events)
	}
}

func TestDevFixtureRejectsAShortPassword(t *testing.T) {
	db, svc, _ := setup(t)
	if _, err := svc.run("short"); err == nil {
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
	if _, err := svc.run(password); err != nil {
		t.Fatal(err)
	}
	if _, err := db.App.Exec(ctx, `UPDATE role_assignments SET valid_to = now()
		WHERE employee_id = (SELECT id FROM employees WHERE email = 'auditor@accessflow.local')`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.run(password); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM role_assignments r JOIN employees e ON e.id = r.employee_id
		WHERE e.email = 'auditor@accessflow.local' AND r.valid_to IS NULL`); n != 0 {
		t.Fatalf("open roles of auditor@ after re-run = %d; want 0 (the removal stands)", n)
	}
}

func TestDevFixtureCatalog(t *testing.T) {
	db, svc, _ := setup(t)
	if _, err := svc.run(password); err != nil {
		t.Fatal(err)
	}
	levels := map[string]string{}
	rows, err := db.App.Query(context.Background(), `
		SELECT t.name || '/' || l.name || '|' || t.fulfillment_method || ' ' || o.email || ' ' || g.name || ' ' ||
		       l.requires_tool_owner || ' ' || l.requires_infosec, t.seeded
		FROM access_levels l JOIN tools t ON t.id = l.tool_id JOIN employees o ON o.id = t.owner_id
		JOIN admin_groups g ON g.id = t.admin_group_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var row string
		var seeded bool
		rows.Scan(&row, &seeded)
		if !seeded {
			t.Errorf("%s is not marked seeded", row)
		}
		name, rest, _ := strings.Cut(row, "|")
		levels[name] = rest
	}
	want := map[string]string{
		"Jira/Viewer":         "API manager@accessflow.local IT Operations false false",
		"Jira/Admin":          "API manager@accessflow.local IT Operations true true",
		"GitHub/Developer":    "MANUAL manager@accessflow.local IT Operations true false",
		"AWS/Admin":           "API infosec2@accessflow.local IT Operations true true",
		"Payroll Portal/User": "VENDOR hr@accessflow.local Vendor Desk true false",
	}
	for name, w := range want {
		if levels[name] != w {
			t.Errorf("%s = %q; want %q", name, levels[name], w)
		}
	}
	if len(levels) != len(want) {
		t.Errorf("levels = %v", levels)
	}
	if n := count(t, db, `SELECT count(*) FROM admin_group_memberships m JOIN admin_groups g ON g.id = m.group_id
		JOIN employees e ON e.id = m.employee_id
		WHERE (g.name, e.email) IN (('IT Operations', 'infosec1@accessflow.local'), ('IT Operations', 'manager@accessflow.local'),
		                            ('Vendor Desk', 'hr@accessflow.local'))`); n != 3 {
		t.Errorf("memberships = %d; want 3", n)
	}
	if n := count(t, db, `SELECT count(*) FROM starter_packages p JOIN access_levels l ON l.id = p.access_level_id
		JOIN tools t ON t.id = l.tool_id
		WHERE p.department = 'Engineering' AND p.job_title IS NULL AND (t.name, l.name) IN (('Jira', 'Viewer'), ('GitHub', 'Developer'))`); n != 2 {
		t.Errorf("Engineering starter package entries = %d; want 2", n)
	}
}

// A tool an administrator already created under a fixture name is left alone.
func TestDevFixtureLeavesExistingToolsAlone(t *testing.T) {
	db, svc, _ := setup(t)
	testutil.CreateTool(t, db, testutil.ToolSpec{Name: "jira", Method: "MANUAL"}) // names are unique ignoring case
	created, err := svc.run(password)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range created {
		if c == "tool Jira" {
			t.Fatal("recreated Jira")
		}
	}
	if n := count(t, db, "SELECT count(*) FROM tools WHERE lower(name) = 'jira' AND fulfillment_method = 'MANUAL' AND NOT seeded"); n != 1 {
		t.Fatal("the existing Jira changed")
	}
	if n := count(t, db, "SELECT count(*) FROM access_levels l JOIN tools t ON t.id = l.tool_id WHERE lower(t.name) = 'jira'"); n != 0 {
		t.Fatal("levels were added to the existing Jira")
	}
}
