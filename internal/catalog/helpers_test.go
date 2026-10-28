package catalog

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// testEnv serves the real auth, catalog and notification routes.
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
	svc := NewService(db.App)
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Mount("/auth", authSvc.Routes())
		r.Group(func(r chi.Router) {
			r.Use(authSvc.RequireSession)
			svc.Register(r)
			notify.Register(r, db.App)
		})
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &testEnv{t: t, db: db, svc: svc, srv: srv}
}

// user creates an active employee with the test password and roles, signed in.
func (e *testEnv) user(roles ...string) (testutil.Employee, *testutil.Client) {
	e.t.Helper()
	return e.login(testutil.EmployeeSpec{Roles: roles})
}

// login creates the employee described by spec with the test password, signed in.
func (e *testEnv) login(spec testutil.EmployeeSpec) (testutil.Employee, *testutil.Client) {
	e.t.Helper()
	spec.Password = true
	emp := testutil.CreateEmployee(e.t, e.db, spec)
	c := testutil.NewClient(e.t, e.srv)
	testutil.RequireStatus(e.t, c.Login(emp.Email, testutil.Password), 200, "")
	return emp, c
}

func (e *testEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.App.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (e *testEnv) audits(action string) int {
	return e.count("SELECT count(*) FROM audit_events WHERE action = $1", action)
}

func (e *testEnv) notifications(recipient uuid.UUID) int {
	return e.count("SELECT count(*) FROM notifications WHERE recipient_id = $1 AND kind = 'APPROVAL_POLICY_CHANGED'", recipient)
}

// newTool creates a tool over the API as admin and returns its id.
func (e *testEnv) newTool(admin *testutil.Client, name, method string) string {
	e.t.Helper()
	owner := testutil.CreateEmployee(e.t, e.db, testutil.EmployeeSpec{})
	r := admin.Do("POST", "/api/v1/tools", map[string]any{"name": name, "fulfillmentMethod": method,
		"ownerId": owner.ID, "adminGroupId": testutil.CreateGroup(e.t, e.db)})
	testutil.RequireStatus(e.t, r, 201, "")
	return r.Body["id"].(string)
}

// newLevel creates a level over the API as admin and returns its body.
func (e *testEnv) newLevel(admin *testutil.Client, tool, name string, toolOwner, infosec bool) map[string]any {
	e.t.Helper()
	r := admin.Do("POST", "/api/v1/tools/"+tool+"/levels", map[string]any{"name": name,
		"requiresToolOwner": toolOwner, "requiresInfosec": infosec})
	testutil.RequireStatus(e.t, r, 201, "")
	return r.Body
}
