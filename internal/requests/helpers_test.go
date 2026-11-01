package requests

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/catalog"
	"github.com/esraahaytaa-eng/access-flow/internal/explorer"
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/reviews"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// world serves the real auth, request and notification routes over an organization:
// skip manages mgr, who manages dev; owner owns every tool; is1 and is2 are InfoSec.
type world struct {
	t       *testing.T
	db      testutil.DB
	svc     *Service
	access  *access.Service // its GitHub adapter is off unless a test sets it
	srv     *httptest.Server
	toolsLn net.Listener // where startTools serves the Mock Tool Service

	dev, mgr, skip, owner, is1, is2, admin  person
	auditor                                 person    // created by auditorOf
	jiraViewer, jiraAdmin, awsAdmin, github uuid.UUID // levels
	jira                                    uuid.UUID // tool
}

type person struct {
	testutil.Employee
	c *testutil.Client
}

func (p person) principal(roles ...string) auth.Principal {
	return auth.Principal{EmployeeID: p.ID, Roles: roles}
}

func newWorld(t *testing.T) *world {
	t.Helper()
	db := testutil.NewDB(t)
	testutil.DefineDepartments(t, db, "Platform", "Engineering")
	testutil.DefinePositions(t, db, "Engineer")
	authSvc, err := auth.NewService(db.App, auth.Options{Secret: []byte(strings.Repeat("k", 32)), BootstrapFile: t.TempDir() + "/admin.txt", LoginRateLimit: 1000, MailKey: testutil.MailKey})
	if err != nil {
		t.Fatal(err)
	}
	istanbul, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(db.App, istanbul)
	toolsLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { toolsLn.Close() })
	accessSvc := access.NewService(db.App, access.NewHTTPToolClient("http://"+toolsLn.Addr().String(), 5*time.Second), istanbul)
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Mount("/auth", authSvc.Routes())
		r.Group(func(r chi.Router) {
			r.Use(authSvc.RequireSession)
			authSvc.RegisterActivations(r)
			svc.Register(r)
			people.NewService(db.App, authSvc).Register(r)
			accessSvc.Register(r)
			notify.Register(r, db.App)
			catalog.NewService(db.App).Register(r)
			reviews.NewService(db.App).Register(r)
			explorer.NewService(db.App, istanbul).Register(r)
		})
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	w := &world{t: t, db: db, svc: svc, access: accessSvc, srv: srv, toolsLn: toolsLn}
	w.skip = w.person(testutil.EmployeeSpec{})
	w.mgr = w.person(testutil.EmployeeSpec{ManagerID: w.skip.ID})
	w.dev = w.person(testutil.EmployeeSpec{ManagerID: w.mgr.ID, Department: "Engineering"})
	w.owner = w.person(testutil.EmployeeSpec{})
	w.is1 = w.person(testutil.EmployeeSpec{Roles: []string{"INFOSEC"}})
	w.is2 = w.person(testutil.EmployeeSpec{Roles: []string{"INFOSEC"}})
	w.admin = w.person(testutil.EmployeeSpec{Roles: []string{"SUPER_ADMIN"}})

	w.jira = testutil.CreateTool(t, db, testutil.ToolSpec{Name: "Jira", OwnerID: w.owner.ID})
	w.jiraViewer = testutil.CreateLevel(t, db, w.jira, "Viewer", false, false)
	w.jiraAdmin = testutil.CreateLevel(t, db, w.jira, "Admin", true, true)
	aws := testutil.CreateTool(t, db, testutil.ToolSpec{Name: "AWS", OwnerID: w.owner.ID})
	w.awsAdmin = testutil.CreateLevel(t, db, aws, "Admin", true, true)
	gh := testutil.CreateTool(t, db, testutil.ToolSpec{Name: "GitHub", OwnerID: w.owner.ID})
	w.github = testutil.CreateLevel(t, db, gh, "Developer", true, false)
	return w
}

// person creates an employee with the test password, signed in.
func (w *world) person(spec testutil.EmployeeSpec) person {
	w.t.Helper()
	spec.Password = true
	emp := testutil.CreateEmployee(w.t, w.db, spec)
	c := testutil.NewClient(w.t, w.srv)
	testutil.RequireStatus(w.t, c.Login(emp.Email, testutil.Password), http.StatusOK, "")
	return person{emp, c}
}

func (w *world) count(sql string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.db.App.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (w *world) exec(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.db.App.Exec(context.Background(), sql, args...); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

func (w *world) audits(action string) int {
	return w.count("SELECT count(*) FROM audit_events WHERE action = $1", action)
}

// item is one request item for level, until revoked.
func item(level uuid.UUID) map[string]any {
	return map[string]any{"accessLevelId": level, "justification": "for the project", "duration": map[string]any{"type": "UNTIL_REVOKED"}}
}

// body is a submission with a fresh submission ID; a nil beneficiary means the submitter.
func body(beneficiary *uuid.UUID, items ...map[string]any) map[string]any {
	b := map[string]any{"submissionId": uuid.New(), "items": append([]map[string]any{}, items...)}
	if beneficiary != nil {
		b["beneficiaryId"] = *beneficiary
	}
	return b
}

func submit(c *testutil.Client, b map[string]any) testutil.Response {
	return c.Do(http.MethodPost, "/api/v1/requests", b)
}

// mustSubmit submits b and returns the created request.
func (w *world) mustSubmit(c *testutil.Client, b map[string]any) map[string]any {
	w.t.Helper()
	r := submit(c, b)
	testutil.RequireStatus(w.t, r, http.StatusCreated, "")
	return r.Body
}

func items(req map[string]any) []map[string]any {
	var out []map[string]any
	for _, it := range req["items"].([]any) {
		out = append(out, it.(map[string]any))
	}
	return out
}

func steps(it map[string]any) []map[string]any {
	var out []map[string]any
	for _, s := range it["steps"].([]any) {
		out = append(out, s.(map[string]any))
	}
	return out
}

// open returns the item's PENDING step.
func open(t *testing.T, it map[string]any) map[string]any {
	t.Helper()
	for _, s := range steps(it) {
		if s["status"] == "PENDING" {
			return s
		}
	}
	t.Fatalf("no pending step in %v", it)
	return nil
}

// decide posts action (approve, reject, override, reassign) on step with its version.
func decide(c *testutil.Client, step map[string]any, action string, extra map[string]any) testutil.Response {
	b := map[string]any{"version": step["version"]}
	for k, v := range extra {
		b[k] = v
	}
	return c.Do(http.MethodPost, "/api/v1/approvals/"+step["id"].(string)+"/"+action, b)
}

// approve approves the step and returns the item.
func (w *world) approve(c *testutil.Client, step map[string]any) map[string]any {
	w.t.Helper()
	r := decide(c, step, "approve", nil)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	return r.Body
}

func (w *world) get(c *testutil.Client, path string) testutil.Response {
	return c.Do(http.MethodGet, path, nil)
}
