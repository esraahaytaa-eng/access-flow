package requests

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// legacy gives account the level in the mock, as if granted before AccessFlow (D10).
func (tl *tools) legacy(t *testing.T, tool, level uuid.UUID, account, status string) {
	t.Helper()
	ctx := context.Background()
	if _, err := tl.db.Exec(ctx, "INSERT INTO accounts (tool, account) VALUES ($1, $2) ON CONFLICT DO NOTHING", tool, account); err != nil {
		t.Fatal(err)
	}
	if _, err := tl.db.Exec(ctx, "INSERT INTO permissions (tool, account, level, status) VALUES ($1, $2, $3, $4)",
		tool, account, level, status); err != nil {
		t.Fatal(err)
	}
}

func (tl *tools) permission(t *testing.T, tool, level uuid.UUID, account string) string {
	t.Helper()
	var status string
	_ = tl.db.QueryRow(context.Background(), "SELECT status FROM permissions WHERE tool = $1 AND account = $2 AND level = $3",
		tool, account, level).Scan(&status)
	return status
}

// orphans are the orphan accounts in Exceptions.
func (w *world) orphans() []map[string]any {
	w.t.Helper()
	r := w.exceptions(w.is1)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	var out []map[string]any
	for _, o := range r.Body["orphanAccounts"].([]any) {
		out = append(out, o.(map[string]any))
	}
	return out
}

func (w *world) toolOf(level uuid.UUID) uuid.UUID {
	w.t.Helper()
	var tool uuid.UUID
	if err := w.db.App.QueryRow(context.Background(), "SELECT tool_id FROM access_levels WHERE id = $1", level).Scan(&tool); err != nil {
		w.t.Fatal(err)
	}
	return tool
}

func TestMockImportMatchesByEmailAndIsIdempotent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	leaver := w.person(testutil.EmployeeSpec{})
	away := w.person(testutil.EmployeeSpec{})
	w.exec("UPDATE employees SET status = 'TERMINATED' WHERE id = $1", leaver.ID)
	w.exec("UPDATE employees SET status = 'SUSPENDED' WHERE id = $1", away.ID)
	gh := w.toolOf(w.github)
	tl.legacy(t, w.jira, w.jiraViewer, strings.ToUpper(w.dev.Email), "ACTIVE") // matched case-insensitively
	tl.legacy(t, gh, w.github, leaver.Email, "ACTIVE")
	tl.legacy(t, gh, w.github, away.Email, "ACTIVE")
	tl.legacy(t, gh, w.github, "ghost@example.com", "ACTIVE")
	tl.legacy(t, w.jira, uuid.New(), w.dev.Email, "ACTIVE") // a level AccessFlow does not know

	testutil.RequireStatus(t, w.owner.c.Do(http.MethodPost, "/api/v1/imports/mock", map[string]any{}), http.StatusForbidden, "forbidden")
	r := w.admin.c.Do(http.MethodPost, "/api/v1/imports/mock", map[string]any{})
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	if r.Body["imported"] != float64(4) || r.Body["orphans"] != float64(1) || r.Body["terminated"] != float64(1) || r.Body["skipped"] != float64(0) {
		t.Fatalf("report = %v", r.Body)
	}
	if rejected := r.Body["rejected"].([]any); len(rejected) != 1 || rejected[0].(map[string]any)["reason"] != "unknown_level" {
		t.Fatalf("rejected = %v", rejected)
	}

	g := w.myGrant(w.dev.c, w.jiraViewer)
	if g["status"] != "ACTIVE" || g["imported"] != true || g["effectiveFrom"] != nil {
		t.Fatalf("imported grant = %v", g)
	}
	if n := w.count("SELECT count(*) FROM grants WHERE import_id IS NOT NULL AND request_item_id IS NULL"); n != 4 {
		t.Fatalf("imported grants = %d", n)
	}
	if w.count("SELECT count(*) FROM grants WHERE employee_id = $1 AND status = 'REVOCATION_REQUESTED'", leaver.ID) != 1 {
		t.Fatal("a terminated employee's account is not being removed")
	}
	tl.drain(t)
	if tl.permission(t, gh, w.github, leaver.Email) != "" || tl.permission(t, gh, w.github, away.Email) != "SUSPENDED" {
		t.Fatal("the leaver's account not revoked or the suspended employee's not suspended")
	}
	if o := w.orphans(); len(o) != 1 || o[0]["account"] != "ghost@example.com" || o[0]["level"] != "Developer" {
		t.Fatalf("orphans = %v", o)
	}
	if w.audits("ACCESS_IMPORTED") != 1 || w.audits("GRANT_IMPORTED") != 4 {
		t.Fatal("import not audited")
	}

	again := w.admin.c.Do(http.MethodPost, "/api/v1/imports/mock", map[string]any{})
	testutil.RequireStatus(t, again, http.StatusCreated, "")
	if again.Body["imported"] != float64(0) || again.Body["skipped"] != float64(3) { // the leaver's account is gone
		t.Fatalf("second import = %v", again.Body)
	}
	if list := w.get(w.admin.c, "/api/v1/imports"); len(list.Body["imports"].([]any)) != 2 {
		t.Fatalf("imports = %v", list.Body)
	}
}

func TestCSVImportForManualTools(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	figma := testutil.CreateTool(t, w.db, testutil.ToolSpec{Name: "Figma", Method: "MANUAL", OwnerID: w.owner.ID})
	editor := testutil.CreateLevel(t, w.db, figma, "Editor", false, false)
	csv := "eposta;arac;seviye;hesap\n" +
		strings.ToUpper(w.dev.Email) + ";figma;Editor;dev-figma\n" +
		"nobody@example.com;Figma;Editor;old-contractor\n" +
		w.dev.Email + ";Sketch;Editor;x\n" +
		w.dev.Email + ";Figma;Owner;x\n" +
		w.dev.Email + ";Jira;Viewer;x\n" + // an API tool: read from the tool itself
		"only;two\n"
	testutil.RequireStatus(t, w.owner.c.Do(http.MethodPost, "/api/v1/imports/csv", map[string]any{"fileName": "figma.csv", "content": csv}),
		http.StatusForbidden, "forbidden")
	r := w.admin.c.Do(http.MethodPost, "/api/v1/imports/csv", map[string]any{"fileName": "figma.csv", "content": csv})
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	if r.Body["imported"] != float64(2) || r.Body["orphans"] != float64(1) {
		t.Fatalf("report = %v", r.Body)
	}
	reasons := map[float64]string{}
	for _, x := range r.Body["rejected"].([]any) {
		reasons[x.(map[string]any)["line"].(float64)] = x.(map[string]any)["reason"].(string)
	}
	if want := map[float64]string{4: "unknown_tool", 5: "unknown_level", 6: "api_tool", 7: "invalid_row"}; len(reasons) != len(want) ||
		reasons[4] != want[4] || reasons[5] != want[5] || reasons[6] != want[6] || reasons[7] != want[7] {
		t.Fatalf("rejected = %v", reasons)
	}
	g := w.myGrant(w.dev.c, editor)
	if g["status"] != "ACTIVE" || g["method"] != "MANUAL" {
		t.Fatalf("imported grant = %v", g)
	}
	if w.count("SELECT count(*) FROM grants WHERE account = 'dev-figma' AND employee_id = $1", w.dev.ID) != 1 {
		t.Fatal("original account identifier not kept")
	}
	if again := w.admin.c.Do(http.MethodPost, "/api/v1/imports/csv", map[string]any{"fileName": "figma.csv", "content": csv}); again.Body["skipped"] != float64(2) {
		t.Fatalf("second import = %v", again.Body)
	}

	for _, bad := range []string{"", "email;tool;level;account\nx;y;z;w\n", "eposta;arac;seviye;hesap\n\"unterminated;a;b;c\n"} {
		testutil.RequireStatus(t, w.admin.c.Do(http.MethodPost, "/api/v1/imports/csv", map[string]any{"fileName": "x.csv", "content": bad}),
			http.StatusBadRequest, "invalid_csv")
	}
}

func TestOrphanAccountsAreRevokedOrDismissed(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	gh := w.toolOf(w.github)
	tl.legacy(t, gh, w.github, "ghost@example.com", "ACTIVE")
	tl.legacy(t, w.jira, w.jiraViewer, "temp@example.com", "ACTIVE")
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodPost, "/api/v1/imports/mock", map[string]any{}), http.StatusCreated, "")
	byAccount := map[string]map[string]any{}
	for _, o := range w.orphans() {
		byAccount[o["account"].(string)] = o
	}
	ghost, temp := byAccount["ghost@example.com"], byAccount["temp@example.com"]
	act := func(p person, o map[string]any, action, reason string) testutil.Response {
		return p.c.Do(http.MethodPost, "/api/v1/orphans/"+o["id"].(string)+"/"+action, map[string]any{"reason": reason, "version": o["version"]})
	}

	testutil.RequireStatus(t, act(w.owner, ghost, "revoke", "unknown account"), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, act(w.is1, ghost, "revoke", " "), http.StatusBadRequest, "reason_required")
	testutil.RequireStatus(t, act(w.is1, ghost, "revoke", "nobody owns it"), http.StatusOK, "")
	tl.drain(t)
	if tl.permission(t, gh, w.github, "ghost@example.com") != "" {
		t.Fatal("orphan account not revoked in the tool")
	}
	testutil.RequireStatus(t, act(w.is1, temp, "dismiss", ""), http.StatusBadRequest, "reason_required")
	testutil.RequireStatus(t, act(w.is1, temp, "dismiss", "the shared kiosk account, kept on purpose"), http.StatusOK, "")
	testutil.RequireStatus(t, act(w.is2, temp, "revoke", "late"), http.StatusConflict, "version_conflict")
	if o := w.orphans(); len(o) != 0 {
		t.Fatalf("orphans left = %v", o)
	}
	if tl.permission(t, w.jira, w.jiraViewer, "temp@example.com") != "ACTIVE" {
		t.Fatal("a dismissed orphan was removed")
	}
	if w.audits("ORPHAN_DISMISSED") != 1 || w.audits("GRANT_REVOKED") != 1 {
		t.Fatal("orphan actions not audited")
	}
}

func TestImportedGrantsAreReviewedIndividually(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	tl.legacy(t, w.jira, w.jiraViewer, w.dev.Email, "ACTIVE")
	tl.legacy(t, w.jira, w.jiraViewer, "ghost@example.com", "ACTIVE")
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodPost, "/api/v1/imports/mock", map[string]any{}), http.StatusCreated, "")
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	items := w.reviewItems(w.is1)
	if len(items) != 1 {
		t.Fatalf("items = %d, want dev's only (orphans are not reviewed)", len(items))
	}
	if flags := items[0]["flags"].([]any); len(flags) != 1 || flags[0] != "IMPORTED_BASELINE" {
		t.Fatalf("flags = %v", items[0]["flags"])
	}
}

func TestChangeOfAnOrphanGrantIsNotFound(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	tl.legacy(t, w.jira, w.jiraViewer, "ghost@example.com", "ACTIVE")
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodPost, "/api/v1/imports/mock", map[string]any{}), http.StatusCreated, "")
	o := w.orphans()[0]
	r := w.dev.c.Do(http.MethodPost, "/api/v1/grants/"+o["id"].(string)+"/change", map[string]any{"submissionId": uuid.New(),
		"accessLevelId": w.jiraAdmin, "justification": "needs more", "duration": map[string]any{"type": "UNTIL_REVOKED"}})
	testutil.RequireStatus(t, r, http.StatusNotFound, "not_found")
}

// An orphan account's grant has no employee: the generic revoke never fails on it (it once
// answered 500). Nobody may give it up as their own or their report's; a Super Admin is told
// to use the orphan finding, which InfoSec revokes or dismisses (D10). Nothing is removed.
func TestDirectRevokeOfAnOrphanGrantPointsToTheFinding(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	tl.legacy(t, w.jira, w.jiraViewer, "ghost@example.com", "ACTIVE")
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodPost, "/api/v1/imports/mock", map[string]any{}), http.StatusCreated, "")
	o := w.orphans()[0]
	revoke := func(p person) testutil.Response {
		return p.c.Do(http.MethodPost, "/api/v1/grants/"+o["id"].(string)+"/revoke", map[string]any{"version": o["version"], "reason": "unknown account"})
	}

	testutil.RequireStatus(t, revoke(w.dev), http.StatusNotFound, "not_found")
	testutil.RequireStatus(t, revoke(w.admin), http.StatusConflict, "orphan_account")
	if w.count("SELECT count(*) FROM operations WHERE grant_id = $1", o["id"]) != 0 || len(w.orphans()) != 1 {
		t.Fatal("the direct revoke changed the orphan")
	}
	if tl.permission(t, w.jira, w.jiraViewer, "ghost@example.com") != "ACTIVE" {
		t.Fatal("the orphan account was touched")
	}
}

// A terminated employee's imported access is already being removed (TERMINATION); a direct
// revoke returns that state and queues nothing more.
func TestDirectRevokeOfATerminatedEmployeesGrantReturnsTheRemoval(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	tl.legacy(t, w.jira, w.jiraViewer, w.dev.Email, "ACTIVE")
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodPost, "/api/v1/imports/mock", map[string]any{}), http.StatusCreated, "")
	w.mustLifecycle(w.hr(), w.dev.ID, "terminate")
	var grant uuid.UUID
	var version int
	if err := w.db.App.QueryRow(context.Background(), "SELECT id, version FROM grants WHERE employee_id = $1", w.dev.ID).Scan(&grant, &version); err != nil {
		t.Fatal(err)
	}
	r := w.admin.c.Do(http.MethodPost, "/api/v1/grants/"+grant.String()+"/revoke", map[string]any{"version": version, "reason": "left"})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["status"] != "REVOCATION_REQUESTED" || w.count("SELECT count(*) FROM operations WHERE grant_id = $1 AND type = 'REVOKE'", grant) != 1 {
		t.Fatalf("grant = %v; want one TERMINATION removal in progress", r.Body)
	}
}

// reported records a spreadsheet-reported baseline grant, as the workbook import does.
func (w *world) reported(employee uuid.UUID, level uuid.UUID, account string) uuid.UUID {
	w.t.Helper()
	var imp, grant uuid.UUID
	if err := w.db.App.QueryRow(context.Background(), `INSERT INTO imports (source, file_name, imported_by, imported_at, report)
		VALUES ('WORKBOOK', 'onboarding.xlsx', $1, now(), '{}') RETURNING id`, w.admin.ID).Scan(&imp); err != nil {
		w.t.Fatal(err)
	}
	if err := w.db.App.QueryRow(context.Background(), `INSERT INTO grants (employee_id, tool_id, access_level_id, method, status, import_id, account, created_at)
		VALUES ($1, $2, $3, 'API', 'ACTIVE', $4, $5, now()) RETURNING id`, employee, w.toolOf(level), level, imp, account).Scan(&grant); err != nil {
		w.t.Fatal(err)
	}
	return grant
}

// The tool read tells spreadsheet-reported API access apart from access the tool holds: it
// verifies a reported grant it finds and counts the ones it does not (D10, M17).
func TestToolReadVerifiesReportedAccess(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	found := w.reported(w.dev.ID, w.jiraViewer, w.dev.Email)
	missing := w.reported(w.dev.ID, w.github, w.dev.Email)
	tl.legacy(t, w.jira, w.jiraViewer, w.dev.Email, "ACTIVE")
	tl.legacy(t, w.jira, w.jiraViewer, w.owner.Email, "ACTIVE")

	r := w.admin.c.Do(http.MethodPost, "/api/v1/imports/mock", map[string]any{})
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	if r.Body["imported"] != float64(1) || r.Body["skipped"] != float64(1) || r.Body["verified"] != float64(1) || r.Body["unverified"] != float64(1) {
		t.Fatalf("report = %v", r.Body)
	}
	if n := w.count("SELECT count(*) FROM grants WHERE id = $1 AND tool_verified_at IS NOT NULL", found); n != 1 {
		t.Fatal("the reported grant the tool holds is not verified")
	}
	if n := w.count("SELECT count(*) FROM grants WHERE id = $1 AND tool_verified_at IS NULL", missing); n != 1 {
		t.Fatal("a reported grant the tool does not hold was verified")
	}
	if n := w.count("SELECT count(*) FROM grants WHERE employee_id = $1 AND import_id IS NOT NULL AND tool_verified_at IS NOT NULL", w.owner.ID); n != 1 {
		t.Fatal("a grant read from the tool is not verified")
	}
	if n := w.count("SELECT count(*) FROM audit_events WHERE action = 'GRANT_VERIFIED_BY_TOOL' AND entity_id = $1", found); n != 1 {
		t.Fatalf("GRANT_VERIFIED_BY_TOOL events = %d; want 1", n)
	}
	if g := w.myGrant(w.dev.c, w.jiraViewer); g["toolVerifiedAt"] == nil {
		t.Fatalf("grant = %v; want toolVerifiedAt", g)
	}
	if g := w.myGrant(w.dev.c, w.github); g["toolVerifiedAt"] != nil {
		t.Fatalf("grant = %v; want no toolVerifiedAt", g)
	}

	// Reading again verifies nothing new, and still counts the grant the tool does not hold.
	r = w.admin.c.Do(http.MethodPost, "/api/v1/imports/mock", map[string]any{})
	if r.Body["verified"] != float64(0) || r.Body["unverified"] != float64(1) || r.Body["skipped"] != float64(2) {
		t.Fatalf("second report = %v", r.Body)
	}
}

// Regression (ENG-001): a failed removal of an orphan account has no employee; retrying it
// used to fail with 500.
func TestFailedOrphanRemovalCanBeRetried(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	gh := w.toolOf(w.github)
	tl.legacy(t, gh, w.github, "ghost@example.com", "ACTIVE")
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodPost, "/api/v1/imports/mock", map[string]any{}), http.StatusCreated, "")
	o := w.orphans()[0]
	tl.faults.RespondNext(http.StatusConflict, `{"error":"level_suspended"}`)
	testutil.RequireStatus(t, w.is1.c.Do(http.MethodPost, "/api/v1/orphans/"+o["id"].(string)+"/revoke",
		map[string]any{"reason": "nobody owns it", "version": o["version"]}), http.StatusOK, "")
	tl.drain(t)
	testutil.RequireStatus(t, retry(w.admin, w.failedOp(w.admin)), http.StatusOK, "")
	tl.drain(t)
	if tl.permission(t, gh, w.github, "ghost@example.com") != "" {
		t.Fatal("orphan account not revoked after the retry")
	}
}
