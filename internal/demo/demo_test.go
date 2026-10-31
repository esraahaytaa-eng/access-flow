package demo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/catalog"
	"github.com/esraahaytaa-eng/access-flow/internal/mocktools"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/requests"
	"github.com/esraahaytaa-eng/access-flow/internal/reviews"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

const passwords = "admin=Demo-Admin-Pass-1,infosec=Demo-InfoSec-Pass-2,hr=Demo-HR-Pass-3,auditor=Demo-Audit-Pass-4," +
	"manager=Demo-Manager-Pass-5,owner=Demo-Owner-Pass-6,employee=Demo-Employee-Pass-7"

func TestParsePasswords(t *testing.T) {
	if got, err := ParsePasswords(passwords); err != nil || len(got) != 7 || got["owner"] != "Demo-Owner-Pass-6" {
		t.Fatalf("got %v, %v", got, err)
	}
	// A malformed pair never reaches the startup log with its password.
	if _, err := ParsePasswords(strings.Replace(passwords, "admin=", "admin:", 1)); err == nil || strings.Contains(err.Error(), "Demo-Admin-Pass-1") {
		t.Errorf("malformed pair: %v", err)
	}
	for _, bad := range []string{
		"",
		strings.Replace(passwords, ",employee=Demo-Employee-Pass-7", "", 1),                          // an account missing
		strings.Replace(passwords, "Demo-Employee-Pass-7", "Demo-Admin-Pass-1", 1),                   // shared password
		strings.Replace(passwords, "Demo-Employee-Pass-7", "short", 1),                               // too weak
		passwords + ",intern=Demo-Intern-Pass-8",                                                     // unknown account
		passwords + ",admin=Demo-Other-Pass-9",                                                       // a key twice
		strings.Replace(passwords, "employee=Demo-Employee-Pass-7", "employeeDemo-Employee-Pass", 1), // no "="
	} {
		if _, err := ParsePasswords(bad); err == nil {
			t.Errorf("ParsePasswords(%q) accepted", bad)
		}
	}
}

type env struct {
	db  testutil.DB
	svc Services
	srv *httptest.Server
}

func setup(t *testing.T) env {
	t.Helper()
	db := testutil.NewDB(t)
	authSvc, err := auth.NewService(db.App, auth.Options{Secret: []byte(strings.Repeat("k", 32)), BootstrapFile: t.TempDir() + "/a.txt", LoginRateLimit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	mock := httptest.NewServer(mocktools.Handler(testutil.NewMockToolsDB(t, mocktools.Migrations)))
	t.Cleanup(mock.Close)
	tools := access.NewHTTPToolClient(mock.URL, 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { access.NewRunner(db.App, tools).Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Mount("/api/v1/auth", authSvc.Routes())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return env{db, Services{Pool: db.App, Auth: authSvc, People: people.NewService(db.App, authSvc), Catalog: catalog.NewService(db.App),
		Requests: requests.NewService(db.App, loc), Access: access.NewService(db.App, tools, loc), Reviews: reviews.NewService(db.App),
		Tools: tools, Location: loc}, srv}
}

func (e env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.App.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSeedCreatesARealisticIdempotentDemo(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	pw, err := ParsePasswords(passwords)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	// As at startup: the demo Super Admin first, then the seed.
	if err := e.svc.Auth.Bootstrap(ctx, "admin@accessflow.local", &auth.DemoAdmin{FullName: AdminName, Password: pw["admin"]}); err != nil {
		t.Fatal(err)
	}
	if err := Seed(ctx, e.svc, "admin@accessflow.local", pw); err != nil {
		t.Fatal(err)
	}

	// Each documented account signs in with its own password, ready to use.
	c := testutil.NewClient(t, e.srv)
	for _, a := range Accounts {
		r := c.Login(a.Email, pw[a.Key])
		if r.Status != http.StatusOK || r.Body["mustChangePassword"] != false {
			t.Errorf("%s: %d %v", a.Email, r.Status, r.Body)
		}
	}
	// Nobody else has a known password.
	for _, p := range pw {
		if r := c.Login("elif.celik@accessflow.local", p); r.Status == http.StatusOK {
			t.Fatal("a non-account employee signs in with a demo password")
		}
	}

	// Only the documented accounts have a password; the other employees have no credential and
	// no activation link, so the Mock Email Outbox holds no activation email (M18).
	if n := e.count(t, "SELECT count(*) FROM password_credentials"); n != len(Accounts) {
		t.Errorf("passwords = %d; want %d (the documented accounts)", n, len(Accounts))
	}
	if n := e.count(t, "SELECT count(*) FROM activation_tokens") + e.count(t, "SELECT count(*) FROM email_outbox WHERE kind = 'ACTIVATION'"); n != 0 {
		t.Errorf("the seed issued %d activation links or emails; want none", n)
	}

	checks := []struct {
		what string
		sql  string
		min  int
	}{
		{"employees, all seed-marked", "SELECT count(*) FROM employees WHERE seeded", 18},
		{"no unmarked employee", "SELECT count(*) FROM employees WHERE NOT seeded", 0},
		{"tools of every method", "SELECT count(DISTINCT fulfillment_method) FROM tools", 3},
		{"the English company catalog", "SELECT count(*) FROM departments WHERE active AND name IN ('Engineering', 'Human Resources', 'Compliance', 'Management')", 4},
		{"every employee placed in it", "SELECT count(*) FROM employees e JOIN departments d ON d.name = e.department JOIN job_positions p ON p.name = e.job_title", 17},
		{"English access levels", "SELECT count(*) FROM access_levels l JOIN tools t ON t.id = l.tool_id WHERE (t.name, l.name) IN (('GitHub', 'Developer'), ('Jira', 'User'))", 2},
		{"two InfoSec members (F2)", "SELECT count(*) FROM role_assignments WHERE role = 'INFOSEC' AND valid_to IS NULL", 2},
		{"approvals waiting at each step type", "SELECT count(DISTINCT type) FROM approval_steps WHERE status = 'PENDING'", 3},
		{"a rejected request", "SELECT count(*) FROM request_items WHERE status = 'REJECTED'", 1},
		{"a withdrawn request", "SELECT count(*) FROM request_items WHERE status = 'WITHDRAWN'", 1},
		{"verified API access", "SELECT count(*) FROM grants WHERE status = 'ACTIVE' AND activation_evidence = 'VERIFIED' AND import_id IS NULL", 2},
		{"confirmed Manual access", "SELECT count(*) FROM grants WHERE status = 'ACTIVE' AND activation_evidence = 'SELF_REPORTED'", 1},
		{"temporary access", "SELECT count(*) FROM grants WHERE status = 'ACTIVE' AND expires_on IS NOT NULL", 1},
		{"a direct grant (D1)", "SELECT count(*) FROM requests WHERE kind = 'DIRECT_GRANT'", 1},
		{"an open Manual task", "SELECT count(*) FROM operations o JOIN grants g ON g.id = o.grant_id WHERE o.status = 'PENDING' AND g.method = 'MANUAL'", 1},
		{"a vendor ticket", "SELECT count(*) FROM operations WHERE ticket_ref IS NOT NULL", 1},
		{"a failed operation", "SELECT count(*) FROM operations WHERE status = 'FAILED'", 1},
		{"imported access", "SELECT count(*) FROM grants WHERE import_id IS NOT NULL AND employee_id IS NOT NULL", 1},
		{"orphan accounts", "SELECT count(*) FROM grants WHERE import_id IS NOT NULL AND employee_id IS NULL", 2},
		{"a suspended employee", "SELECT count(*) FROM employees WHERE status = 'SUSPENDED'", 1},
		{"a terminated employee with an open removal", `SELECT count(*) FROM operations o JOIN grants g ON g.id = o.grant_id
			JOIN employees x ON x.id = g.employee_id WHERE x.status = 'TERMINATED' AND o.type = 'REVOKE' AND o.status = 'PENDING'`, 1},
		{"a scheduled termination", "SELECT count(*) FROM employees WHERE termination_at IS NOT NULL", 1},
		{"a running review campaign", "SELECT count(*) FROM review_campaigns WHERE closed_at IS NULL", 1},
		{"a review decision", "SELECT count(*) FROM review_items WHERE decision IS NOT NULL", 1},
	}
	for _, ch := range checks {
		if n := e.count(t, ch.sql); (ch.min == 0 && n != 0) || n < ch.min {
			t.Errorf("%s: %d, want at least %d", ch.what, n, ch.min)
		}
	}
	// Real time only (D12): no event predates the seed.
	if n := e.count(t, "SELECT count(*) FROM audit_events WHERE occurred_at < $1", start); n != 0 {
		t.Errorf("%d audit events before the seed ran", n)
	}

	// A second start changes nothing.
	before := e.count(t, "SELECT count(*) FROM audit_events WHERE action NOT LIKE 'LOGIN%'")
	if err := Seed(ctx, e.svc, "admin@accessflow.local", pw); err != nil {
		t.Fatal(err)
	}
	if after := e.count(t, "SELECT count(*) FROM audit_events WHERE action NOT LIKE 'LOGIN%'"); after != before {
		t.Fatalf("second run wrote %d audit events", after-before)
	}
}

// The seed runs once, on a database holding only the demo Super Admin. Anything else (an
// interrupted first start, a database from an earlier version) is refused with the reset step.
func TestSeedRefusesADatabaseItDidNotFinish(t *testing.T) {
	pw, err := ParsePasswords(passwords)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	demoAdmin := &auth.DemoAdmin{FullName: AdminName, Password: pw["admin"]}
	for name, prepare := range map[string]func(e env) error{
		"other data, no marker": func(e env) error {
			testutil.CreateEmployee(t, e.db, testutil.EmployeeSpec{})
			return e.svc.Auth.Bootstrap(ctx, "admin@accessflow.local", demoAdmin)
		},
		"initial Super Admin from an earlier version": func(e env) error { return e.svc.Auth.Bootstrap(ctx, "admin@accessflow.local", nil) },
	} {
		t.Run(name, func(t *testing.T) {
			e := setup(t)
			if err := prepare(e); err != nil {
				t.Fatal(err)
			}
			err := Seed(ctx, e.svc, "admin@accessflow.local", pw)
			if err == nil || !strings.Contains(err.Error(), "docker compose down -v") {
				t.Fatalf("err = %v; want the reset instruction", err)
			}
			if n := e.count(t, "SELECT count(*) FROM requests"); n != 0 {
				t.Fatalf("%d requests created", n)
			}
		})
	}
}
