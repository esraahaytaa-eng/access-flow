package onboarding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/catalog"
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

type env struct {
	t               testing.TB
	db              testutil.DB
	srv             *httptest.Server
	admin           testutil.Employee
	sa, hr, infosec *testutil.Client
}

// newEnv is a company with a Super Admin, an HR Manager, an InfoSec member, the catalog
// (Engineering, Backend Developer, IT Operations) and two tools: Jira (Manual, level User)
// and AWS (API, level ReadOnly).
func newEnv(t *testing.T) *env {
	t.Helper()
	db := testutil.NewDB(t)
	authSvc, err := auth.NewService(db.App, auth.Options{Secret: []byte(strings.Repeat("k", 32)), BootstrapFile: t.TempDir() + "/admin.txt", LoginRateLimit: 1000, MailKey: testutil.MailKey})
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Mount("/auth", authSvc.Routes())
		r.Group(func(r chi.Router) {
			r.Use(authSvc.RequireSession)
			authSvc.RegisterActivations(r)
			NewService(db.App, authSvc).Register(r)
			people.NewService(db.App, authSvc).Register(r)
			catalog.NewService(db.App).Register(r)
			access.NewService(db.App, nil, time.UTC).Register(r)
			notify.Register(r, db.App)
		})
	})
	e := &env{t: t, db: db, srv: httptest.NewServer(r)}
	t.Cleanup(e.srv.Close)
	e.admin, e.sa = e.user(testutil.EmployeeSpec{Email: "admin@co.test", Roles: []string{"SUPER_ADMIN"}})
	_, e.hr = e.user(testutil.EmployeeSpec{Roles: []string{"HR_MANAGER"}})
	_, e.infosec = e.user(testutil.EmployeeSpec{Roles: []string{"INFOSEC"}})
	testutil.DefineDepartments(t, db, "Engineering")
	testutil.DefinePositions(t, db, "Backend Developer")
	e.exec("INSERT INTO admin_groups (name) VALUES ('IT Operations')")
	jira := testutil.CreateTool(t, db, testutil.ToolSpec{Name: "Jira", Method: "MANUAL", OwnerID: e.admin.ID})
	testutil.CreateLevel(t, db, jira, "User", false, false)
	aws := testutil.CreateTool(t, db, testutil.ToolSpec{Name: "AWS", Method: "API", OwnerID: e.admin.ID})
	testutil.CreateLevel(t, db, aws, "ReadOnly", false, true)
	return e
}

func (e *env) user(spec testutil.EmployeeSpec) (testutil.Employee, *testutil.Client) {
	e.t.Helper()
	spec.Password = true
	emp := testutil.CreateEmployee(e.t, e.db, spec)
	c := testutil.NewClient(e.t, e.srv)
	testutil.RequireStatus(e.t, c.Login(emp.Email, testutil.Password), http.StatusOK, "")
	return emp, c
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.db.App.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.App.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// onboardingFile is two new employees, a new tool with two levels, and four accesses across
// the new tool, a Manual tool and an API tool; edit changes it.
func onboardingFile(t *testing.T, edit func(f *excelize.File)) []byte {
	return newWorkbook(t, func(f *excelize.File) {
		row(t, f, SheetEmployees, "A2", "ann@co.test", "Ann Lee", "Engineering", "Backend Developer", "bob@co.test")
		row(t, f, SheetEmployees, "A3", "bob@co.test", "Bob Ray", "Engineering", "", "admin@co.test")
		row(t, f, SheetTools, "A2", "GitHub", "Manual", "bob@co.test", "IT Operations", "Developer", "Manager + Tool Owner")
		row(t, f, SheetTools, "A3", "GitHub", "Manual", "bob@co.test", "IT Operations", "Admin", "Manager + Tool Owner + InfoSec")
		row(t, f, SheetAccess, "A2", "ann@co.test", "GitHub", "Developer", "")
		row(t, f, SheetAccess, "A3", "ann@co.test", "Jira", "User", "ann.lee")
		row(t, f, SheetAccess, "A4", "bob@co.test", "GitHub", "Admin", "")
		row(t, f, SheetAccess, "A5", "ann@co.test", "AWS", "ReadOnly", "")
		if edit != nil {
			edit(f)
		}
	})
}

const (
	previewPath = "/api/v1/imports/workbook/preview"
	importPath  = "/api/v1/imports/workbook?fileName=onboarding.xlsx"
)

func summary(t *testing.T, r testutil.Response) map[string]any {
	t.Helper()
	plan, _ := r.Body["plan"].(map[string]any)
	if plan == nil {
		plan = r.Body
	}
	s, _ := plan["summary"].(map[string]any)
	if s == nil {
		t.Fatalf("no summary in %v", r.Body)
	}
	return s
}

func TestOnlySuperAdminOnboards(t *testing.T) {
	e := newEnv(t)
	file := onboardingFile(t, nil)
	testutil.RequireStatus(t, e.hr.Do(http.MethodGet, "/api/v1/imports/workbook/template", nil), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, e.hr.Do(http.MethodPost, previewPath, file), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, e.infosec.Do(http.MethodPost, importPath, file), http.StatusForbidden, "forbidden")
	if n := e.count("SELECT count(*) FROM employees WHERE email IN ('ann@co.test', 'bob@co.test')"); n != 0 {
		t.Fatalf("employees created = %d", n)
	}
}

func TestTemplateDownload(t *testing.T) {
	e := newEnv(t)
	r := e.sa.Do(http.MethodGet, "/api/v1/imports/workbook/template", nil)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if ct := r.Header.Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("content type = %q", ct)
	}
	if cd := r.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="accessflow-onboarding-`) {
		t.Fatalf("content disposition = %q", cd)
	}
	wb, problems := Parse(r.Raw)
	if len(problems) != 0 || len(wb.Employees) != 1 || wb.Employees[0].Department != "Engineering" {
		t.Fatalf("template = %+v, %v", wb, problems)
	}
}

func TestPreviewWritesNothing(t *testing.T) {
	e := newEnv(t)
	before := e.count("SELECT count(*) FROM audit_events")
	r := e.sa.Do(http.MethodPost, previewPath, onboardingFile(t, nil))
	testutil.RequireStatus(t, r, http.StatusOK, "")
	s := summary(t, r)
	if s["employeesNew"] != float64(2) || s["toolsNew"] != float64(1) || s["levelsNew"] != float64(2) || s["accessNew"] != float64(4) {
		t.Fatalf("summary = %v", s)
	}
	if owners := r.Body["owners"].([]any); len(owners) != 1 || owners[0].(map[string]any)["ownerEmail"] != "bob@co.test" {
		t.Fatalf("owners = %v", owners)
	}
	if n := e.count("SELECT count(*) FROM audit_events"); n != before {
		t.Fatalf("audit events %d -> %d", before, n)
	}
	for _, table := range []string{"imports", "grants"} {
		if n := testutil.Count(t, e.db.App, table); n != 0 {
			t.Fatalf("%s = %d after a preview", table, n)
		}
	}
}

func TestWorkbookImport(t *testing.T) {
	e := newEnv(t)
	file := onboardingFile(t, nil)
	r := e.sa.Do(http.MethodPost, importPath, file)
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	rep := r.Body["report"].(map[string]any)
	if rep["imported"] != float64(4) || rep["source"] != "WORKBOOK" || rep["fileName"] != "onboarding.xlsx" {
		t.Fatalf("report = %v", rep)
	}

	// Employees and managers, without a password or a role.
	if n := e.count(`SELECT count(*) FROM employees a JOIN employees b ON b.id = a.manager_id JOIN employees c ON c.id = b.manager_id
		WHERE a.email = 'ann@co.test' AND b.email = 'bob@co.test' AND c.email = 'admin@co.test' AND a.department = 'Engineering'
		  AND a.job_title = 'Backend Developer' AND a.status = 'ACTIVE'`); n != 1 {
		t.Fatal("employees or managers not created")
	}
	if n := e.count(`SELECT count(*) FROM password_credentials c JOIN employees e ON e.id = c.employee_id
		WHERE e.email IN ('ann@co.test', 'bob@co.test')`); n != 0 {
		t.Fatalf("imported employees have %d credentials; want none", n)
	}
	// Each gets one activation link in the Mock Email Outbox, issued by the importer (M18).
	if n := e.count(`SELECT count(*) FROM activation_tokens t JOIN employees e ON e.id = t.employee_id
		JOIN email_outbox o ON o.activation_id = t.id AND o.recipient_id = e.id AND o.sealed_token IS NOT NULL
		WHERE e.email IN ('ann@co.test', 'bob@co.test') AND t.reason = 'IMPORTED' AND t.issued_by = $1`, e.admin.ID); n != 2 {
		t.Fatalf("activation links = %d; want 2", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_events WHERE action = 'ACTIVATION_LINK_ISSUED' AND actor_id = $1
		AND details->>'reason' = 'IMPORTED'`, e.admin.ID); n != 2 {
		t.Fatalf("ACTIVATION_LINK_ISSUED = %d; want 2", n)
	}
	if n := e.count(`SELECT count(*) FROM role_assignments ra JOIN employees e ON e.id = ra.employee_id
		WHERE e.email IN ('ann@co.test', 'bob@co.test')`); n != 0 {
		t.Fatal("the import assigned a role")
	}
	ann := testutil.NewClient(t, e.srv)
	if r := ann.Login("ann@co.test", testutil.Password); r.Status == http.StatusOK {
		t.Fatal("an imported employee signed in without a password")
	}

	// The tool, its owner, group and approval steps.
	if n := e.count(`SELECT count(*) FROM tools t JOIN employees o ON o.id = t.owner_id JOIN admin_groups g ON g.id = t.admin_group_id
		WHERE t.name = 'GitHub' AND t.fulfillment_method = 'MANUAL' AND o.email = 'bob@co.test' AND g.name = 'IT Operations'`); n != 1 {
		t.Fatal("tool not created with its owner and group")
	}
	if n := e.count(`SELECT count(*) FROM access_levels l JOIN tools t ON t.id = l.tool_id WHERE t.name = 'GitHub'
		AND ((l.name = 'Developer' AND l.requires_tool_owner AND NOT l.requires_infosec) OR (l.name = 'Admin' AND l.requires_tool_owner AND l.requires_infosec))`); n != 2 {
		t.Fatal("levels not created with their steps")
	}

	// Existing access as baseline grants: no operation, request or approval; reported, not verified.
	if n := e.count(`SELECT count(*) FROM grants WHERE import_id IS NOT NULL AND request_item_id IS NULL AND status = 'ACTIVE' AND tool_verified_at IS NULL`); n != 4 {
		t.Fatalf("baseline grants = %d; want 4", n)
	}
	if n := e.count("SELECT count(*) FROM grants g JOIN employees e ON e.id = g.employee_id WHERE e.email = 'ann@co.test' AND g.account = 'ann.lee'"); n != 1 {
		t.Fatal("the account identifier was not kept")
	}
	for _, table := range []string{"operations", "requests", "request_items", "approval_steps"} {
		if n := testutil.Count(t, e.db.App, table); n != 0 {
			t.Fatalf("%s = %d; the import must not provision or invent approvals", table, n)
		}
	}

	// Audited one by one; InfoSec gets one notification for the new approval policies.
	for action, want := range map[string]int{"EMPLOYEE_CREATED": 2, "TOOL_CREATED": 1, "ACCESS_LEVEL_CREATED": 2, "GRANT_IMPORTED": 4, "ACCESS_IMPORTED": 1} {
		if n := e.count("SELECT count(*) FROM audit_events WHERE action = $1 AND actor_id = $2", action, e.admin.ID); n != want {
			t.Errorf("%s events = %d; want %d", action, n, want)
		}
	}
	if n := e.count("SELECT count(*) FROM audit_events WHERE action = 'EMPLOYEE_CREATED' AND details ? 'importId'"); n != 2 {
		t.Errorf("EMPLOYEE_CREATED events without the import = %d", 2-n)
	}
	if n := e.count("SELECT count(*) FROM notifications WHERE kind = 'APPROVAL_POLICIES_IMPORTED'"); n != 1 {
		t.Errorf("bulk notifications = %d; want 1", n)
	}
	if n := e.count("SELECT count(*) FROM notifications WHERE kind = 'APPROVAL_POLICY_CHANGED'"); n != 0 {
		t.Errorf("per-level notifications = %d; want 0", n)
	}

	// Importing again creates nothing.
	r = e.sa.Do(http.MethodPost, importPath, file)
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	rep = r.Body["report"].(map[string]any)
	if rep["imported"] != float64(0) || rep["skipped"] != float64(4) {
		t.Fatalf("second report = %v", rep)
	}
	// The preview's access counts predict what the D10 code really skips.
	if s := summary(t, r); s["employeesExisting"] != float64(2) || s["toolsExisting"] != float64(1) || s["levelsExisting"] != float64(2) ||
		s["accessExisting"] != rep["skipped"] || s["accessNew"] != rep["imported"] {
		t.Fatalf("second summary = %v", s)
	}
	if n := e.count("SELECT count(*) FROM notifications WHERE kind = 'APPROVAL_POLICIES_IMPORTED'"); n != 1 {
		t.Errorf("bulk notifications after re-import = %d; want 1", n)
	}

	// The employee sets a password through the link, then signs in.
	var id uuid.UUID
	if err := e.db.App.QueryRow(context.Background(), "SELECT id FROM employees WHERE email = 'ann@co.test'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	testutil.Activate(t, e.sa, e.db.App, id, "violet anchor quiet 29")
	testutil.RequireStatus(t, ann.Login("ann@co.test", "violet anchor quiet 29"), http.StatusOK, "")
}

func problemReasons(r testutil.Response) []string {
	var out []string
	plan, _ := r.Body["plan"].(map[string]any)
	ps, _ := plan["problems"].([]any)
	for _, p := range ps {
		out = append(out, p.(map[string]any)["reason"].(string))
	}
	return out
}

// One bad row anywhere, or a failure while applying, leaves nothing behind.
func TestWorkbookImportIsAllOrNothing(t *testing.T) {
	e := newEnv(t)
	bad := onboardingFile(t, func(f *excelize.File) { row(t, f, SheetAccess, "A6", "bob@co.test", "Jira", "Owner", "") })
	r := e.sa.Do(http.MethodPost, importPath, bad)
	testutil.RequireStatus(t, r, http.StatusBadRequest, "workbook_invalid")
	if got := problemReasons(r); len(got) != 1 || got[0] != "unknown_level" {
		t.Fatalf("problems = %v", got)
	}

	// A failure after the employees and the tool were written rolls them back too.
	if _, err := e.db.Owner.Exec(context.Background(), `CREATE FUNCTION fail_grant() RETURNS trigger LANGUAGE plpgsql AS
		$$BEGIN IF NEW.account = 'ann.lee' THEN RAISE EXCEPTION 'injected failure'; END IF; RETURN NEW; END$$;
		CREATE TRIGGER fail_grant BEFORE INSERT ON grants FOR EACH ROW EXECUTE FUNCTION fail_grant()`); err != nil {
		t.Fatal(err)
	}
	testutil.RequireStatus(t, e.sa.Do(http.MethodPost, importPath, onboardingFile(t, nil)), http.StatusInternalServerError, "")
	for _, sql := range []string{
		"SELECT count(*) FROM employees WHERE email IN ('ann@co.test', 'bob@co.test')",
		"SELECT count(*) FROM tools WHERE name = 'GitHub'",
		"SELECT count(*) FROM grants",
		"SELECT count(*) FROM imports",
		"SELECT count(*) FROM audit_events WHERE action IN ('EMPLOYEE_CREATED', 'TOOL_CREATED', 'GRANT_IMPORTED', 'ACTIVATION_LINK_ISSUED')",
		"SELECT count(*) FROM activation_tokens",
		"SELECT count(*) FROM email_outbox",
	} {
		if n := e.count(sql); n != 0 {
			t.Fatalf("%s = %d after a failed import", sql, n)
		}
	}
}

// The import validates again in its transaction: a value retired after the preview is refused.
func TestImportRevalidates(t *testing.T) {
	e := newEnv(t)
	file := onboardingFile(t, nil)
	testutil.RequireStatus(t, e.sa.Do(http.MethodPost, previewPath, file), http.StatusOK, "")
	e.exec("UPDATE job_positions SET active = false WHERE name = 'Backend Developer'")
	r := e.sa.Do(http.MethodPost, importPath, file)
	testutil.RequireStatus(t, r, http.StatusBadRequest, "workbook_invalid")
	if got := problemReasons(r); len(got) != 1 || got[0] != "retired_job_position" {
		t.Fatalf("problems = %v", got)
	}
}

// Existing records are never overwritten; a terminated employee's account is removed (D10).
func TestExistingRecordsAreKept(t *testing.T) {
	e := newEnv(t)
	gone := testutil.CreateEmployee(t, e.db, testutil.EmployeeSpec{Email: "gone@co.test", Status: "TERMINATED"})
	file := onboardingFile(t, func(f *excelize.File) {
		row(t, f, SheetEmployees, "A4", "admin@co.test", "Someone Else", "Engineering", "", "")
		row(t, f, SheetTools, "A4", "Jira", "Vendor", "bob@co.test", "IT Operations", "User", "Manager + InfoSec")
		row(t, f, SheetAccess, "A6", "gone@co.test", "Jira", "User", "")
	})
	r := e.sa.Do(http.MethodPost, importPath, file)
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	warnings := map[string]bool{}
	for _, w := range r.Body["plan"].(map[string]any)["warnings"].([]any) {
		w := w.(map[string]any)
		warnings[w["reason"].(string)+"/"+w["column"].(string)] = true
	}
	for _, want := range []string{"employee_differs/Full Name", "employee_differs/Department", "tool_differs/Fulfillment Method",
		"tool_differs/Tool Owner Email", "level_differs/Approval Steps", "employee_terminated/Employee Email"} {
		if !warnings[want] {
			t.Errorf("warning %s missing in %v", want, warnings)
		}
	}
	if n := e.count(`SELECT count(*) FROM employees WHERE id = $1 AND full_name = 'Test admin@co.test' AND department IS NULL`, e.admin.ID); n != 1 {
		t.Fatal("an existing employee was changed")
	}
	if n := e.count(`SELECT count(*) FROM tools t JOIN access_levels l ON l.tool_id = t.id WHERE t.name = 'Jira'
		AND t.fulfillment_method = 'MANUAL' AND t.owner_id = $1 AND NOT l.requires_infosec`, e.admin.ID); n != 1 {
		t.Fatal("an existing tool or level was changed")
	}
	if n := e.count("SELECT count(*) FROM grants WHERE employee_id = $1 AND status = 'REVOCATION_REQUESTED'", gone.ID); n != 1 {
		t.Fatal("a terminated employee's account is not being removed")
	}
}
