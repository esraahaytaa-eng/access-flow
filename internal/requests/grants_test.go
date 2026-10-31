package requests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/mocktools"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// tools is the real Mock Tool Service handler and database behind Faults, with a runner.
type tools struct {
	db     *pgxpool.Pool
	faults *testutil.Faults
	runner *access.Runner
}

func (w *world) startTools() *tools {
	w.t.Helper()
	mock := testutil.NewMockToolsDB(w.t, mocktools.Migrations)
	faults := testutil.NewFaults(mocktools.Handler(mock))
	srv := httptest.NewUnstartedServer(faults)
	srv.Listener.Close()
	srv.Listener = w.toolsLn // the address the import routes call
	srv.Start()
	w.t.Cleanup(srv.Close)
	return &tools{mock, faults, access.NewRunner(w.db.App, access.NewHTTPToolClient(srv.URL, testutil.WaitLimit))}
}

// drain runs the runner until nothing is due.
func (tl *tools) drain(t *testing.T) {
	t.Helper()
	for {
		found, err := tl.runner.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			return
		}
	}
}

// held is how many permissions the mock holds for the grant.
func (tl *tools) held(t *testing.T, g map[string]any, email string) int {
	t.Helper()
	var n int
	if err := tl.db.QueryRow(context.Background(), "SELECT count(*) FROM permissions WHERE tool = $1 AND account = $2 AND level = $3",
		g["toolId"], email, g["accessLevelId"]).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// myGrant returns c's grant for level from My Access.
func (w *world) myGrant(c *testutil.Client, level uuid.UUID) map[string]any {
	w.t.Helper()
	r := w.get(c, "/api/v1/access")
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	for _, g := range r.Body["grants"].([]any) {
		if g := g.(map[string]any); g["accessLevelId"] == level.String() {
			return g
		}
	}
	w.t.Fatalf("no grant for %v in %v", level, r.Body)
	return nil
}

func revoke(c *testutil.Client, g map[string]any, reason string) testutil.Response {
	return c.Do(http.MethodPost, "/api/v1/grants/"+g["id"].(string)+"/revoke", map[string]any{"version": g["version"], "reason": reason})
}

func (w *world) mustRevoke(c *testutil.Client, g map[string]any) map[string]any {
	w.t.Helper()
	r := revoke(c, g, "no longer needed")
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	return r.Body
}

func requireGrant(t *testing.T, g map[string]any, status, display string) {
	t.Helper()
	if g["status"] != status || g["displayStatus"] != display {
		t.Fatalf("grant %v / %v; want %s / %s", g["status"], g["displayStatus"], status, display)
	}
}

// The whole CASE flow against the real Mock Tool Service.
func TestVoluntaryRevokeEndToEnd(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	requireGrant(t, g, "ACTIVE", "ACTIVE / VERIFIED")
	if g["effectiveFrom"] == nil || tl.held(t, g, w.dev.Email) != 1 {
		t.Fatalf("activation not verified: %v", g)
	}

	g = w.mustRevoke(w.dev.c, g)
	requireGrant(t, g, "REVOCATION_REQUESTED", "REVOCATION_REQUESTED") // still held (O1)
	tl.drain(t)
	g = w.myGrant(w.dev.c, w.jiraViewer)
	requireGrant(t, g, "REVOKED", "REVOKED / VERIFIED")
	if g["effectiveTo"] == nil || tl.held(t, g, w.dev.Email) != 0 {
		t.Fatalf("removal not verified: %v", g)
	}
	var accounts int
	if err := tl.db.QueryRow(context.Background(), "SELECT count(*) FROM accounts WHERE account = $1", w.dev.Email).Scan(&accounts); err != nil || accounts != 1 {
		t.Fatalf("the account must remain (D4): %d %v", accounts, err)
	}
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'GRANT_REVOCATION_REQUESTED' AND actor_id = $1
		AND reason = 'no longer needed' AND subject_employee_id = $1`, w.dev.ID); n != 1 {
		t.Fatalf("GRANT_REVOCATION_REQUESTED = %d", n)
	}
	if w.audits("GRANT_REVOKED") != 1 {
		t.Fatal("no GRANT_REVOKED")
	}
	if n := w.count("SELECT count(*) FROM operations WHERE type = 'REVOKE' AND reason = 'VOLUNTARY' AND priority = 1"); n != 1 {
		t.Fatalf("voluntary removal operation = %d", n)
	}
	it := items(w.get(w.dev.c, "/api/v1/requests").Body["requests"].([]any)[0].(map[string]any))[0]
	if f := it["fulfillment"].(map[string]any); f["status"] != "REVOKED / VERIFIED" {
		t.Fatalf("request item fulfillment = %v", f)
	}
}

func TestManagerRevokesDirectReport(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	for _, p := range []person{w.skip, w.owner} { // the manager's manager is not the manager
		testutil.RequireStatus(t, revoke(p.c, g, "x"), http.StatusNotFound, "")
	}
	w.exec("UPDATE employees SET manager_id = $2 WHERE id = $1", w.dev.ID, w.skip.ID)
	testutil.RequireStatus(t, revoke(w.mgr.c, g, "x"), http.StatusNotFound, "") // a former manager
	requireGrant(t, w.mustRevoke(w.skip.c, g), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
	if n := w.count("SELECT count(*) FROM operations WHERE type = 'REVOKE' AND reason = 'MANAGER'"); n != 1 {
		t.Fatalf("manager removal operation = %d", n)
	}
}

func TestRevokeWhileQueuedCancels(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	requireGrant(t, w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.jiraViewer)), "CANCELLED", "CANCELLED")
	tl.drain(t)
	if tl.faults.Requests() != 0 {
		t.Fatal("a queued grant reached the tool after it was given up")
	}
	if n := w.count("SELECT count(*) FROM operations WHERE type = 'GRANT' AND status = 'CANCELLED'") +
		w.count("SELECT count(*) FROM operations WHERE type = 'REVOKE'"); n != 1 {
		t.Fatalf("operations: want one cancelled GRANT and no REVOKE, got %d", n)
	}
	if n := w.count("SELECT count(*) FROM audit_events WHERE action = 'GRANT_CANCELLED' AND actor_id = $1 AND reason = 'no longer needed'", w.dev.ID); n != 1 {
		t.Fatalf("GRANT_CANCELLED = %d", n)
	}
}

// The removal arrives while the provisioning call is in flight: the GRANT completes and
// records when access began, then the REVOKE removes it.
func TestRevokeWhileInFlight(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	done := w.inFlight(tl)
	requireGrant(t, w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.jiraViewer)), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
	if err := done(); err != nil {
		t.Fatal(err)
	}
	tl.drain(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	requireGrant(t, g, "REVOKED", "REVOKED / VERIFIED")
	if g["effectiveFrom"] == nil || g["effectiveTo"] == nil || tl.held(t, g, w.dev.Email) != 0 {
		t.Fatalf("want access from activation to removal, none left: %v", g)
	}
}

// The tool applied the grant but the answer was lost; the removal still removes it.
func TestRevokeWhileRetrying(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.faults.ApplyThenFail()
	if _, err := tl.runner.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	g := w.myGrant(w.dev.c, w.jiraViewer)
	if tl.held(t, g, w.dev.Email) != 1 {
		t.Fatal("setup: the tool should hold the level")
	}
	requireGrant(t, w.mustRevoke(w.dev.c, g), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
	w.exec("UPDATE operations SET next_attempt_at = now()")
	tl.drain(t)
	g = w.myGrant(w.dev.c, w.jiraViewer)
	requireGrant(t, g, "REVOKED", "REVOKED / VERIFIED")
	if tl.held(t, g, w.dev.Email) != 0 || g["effectiveFrom"] != nil || g["effectiveTo"] != nil {
		t.Fatalf("never verified active, now verified absent: %v", g)
	}
	if n := w.count("SELECT count(*) FROM operations WHERE type = 'GRANT' AND status = 'CANCELLED'"); n != 1 {
		t.Fatal("the retrying GRANT must be cancelled so it cannot re-apply access")
	}
}

func TestRevokeAfterFailed(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.faults.RespondNext(http.StatusConflict, `{"error":"level_suspended"}`)
	tl.drain(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	requireGrant(t, g, "FAILED", "FAILED")
	requireGrant(t, w.mustRevoke(w.dev.c, g), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
	tl.drain(t)
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "REVOKED", "REVOKED / VERIFIED")
	if n := w.count("SELECT count(*) FROM operations WHERE type = 'GRANT' AND status = 'CANCELLED'"); n != 1 {
		t.Fatal("the failed GRANT must be cancelled so no one retries it after the removal")
	}
}

func TestRepeatedRevokeIsIdempotent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	var wg sync.WaitGroup
	statuses := make(chan int, 5)
	for range 5 {
		wg.Go(func() { statuses <- revoke(w.dev.c, g, "no longer needed").Status }) // all with the same, older version
	}
	wg.Wait()
	close(statuses)
	for s := range statuses {
		if s != http.StatusOK {
			t.Fatalf("status %d", s)
		}
	}
	requireGrant(t, w.mustRevoke(w.dev.c, g), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
	if ops, events := w.count("SELECT count(*) FROM operations WHERE type = 'REVOKE'"), w.audits("GRANT_REVOCATION_REQUESTED"); ops != 1 || events != 1 {
		t.Fatalf("REVOKE operations %d, events %d", ops, events)
	}
}

func TestRevokeInput(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	path := "/api/v1/grants/" + g["id"].(string) + "/revoke"
	testutil.RequireStatus(t, w.dev.c.Do(http.MethodPost, path, map[string]any{"version": g["version"]}), http.StatusBadRequest, "field_required")
	testutil.RequireStatus(t, revoke(w.dev.c, g, "  "), http.StatusBadRequest, "reason_required")
	stale := map[string]any{"id": g["id"], "version": g["version"].(float64) - 1}
	testutil.RequireStatus(t, revoke(w.dev.c, stale, "x"), http.StatusConflict, "version_conflict")
	testutil.RequireStatus(t, revoke(w.dev.c, map[string]any{"id": uuid.NewString(), "version": 1}, "x"), http.StatusNotFound, "")
	testutil.RequireStatus(t, w.dev.c.Do(http.MethodPost, "/api/v1/grants/nope/revoke", map[string]any{"version": 1, "reason": "x"}),
		http.StatusNotFound, "") // a malformed ID is simply not found
}

func TestRevokePermissionMatrix(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	auditor := w.person(testutil.EmployeeSpec{Roles: []string{"AUDITOR"}})
	for _, tc := range []struct {
		name   string
		actor  person
		status int
	}{
		{"the employee", w.dev, http.StatusOK},
		{"the direct manager", w.mgr, http.StatusOK},
		{"a Super Admin", w.admin, http.StatusOK}, // direct revocation (D1)
		{"InfoSec", w.is1, http.StatusNotFound},
		{"an Auditor", auditor, http.StatusForbidden}, // audit events only (O7 note 2026-10-31)
	} {
		t.Run(tc.name, func(t *testing.T) {
			w.exec("UPDATE grants SET status = 'CANCELLED' WHERE status <> 'CANCELLED'")
			w.approved(w.jiraViewer)
			// As the runner leaves a verified activation.
			w.exec(`UPDATE operations SET status = 'SUCCEEDED', attempts = 1, first_attempt_at = now(), completed_at = now()
				WHERE type = 'GRANT' AND status = 'PENDING'`)
			w.exec("UPDATE grants SET status = 'ACTIVE', activation_evidence = 'VERIFIED', effective_from = now() WHERE status = 'PROVISIONING'")
			testutil.RequireStatus(t, revoke(tc.actor.c, w.myGrant(w.dev.c, w.jiraViewer), "x"), tc.status, "")
		})
	}
}

func TestMyAccessAndTeamVisibility(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.approved(w.jiraViewer)                                                                     // dev's
	w.approve(w.skip.c, open(t, items(w.mustSubmit(w.mgr.c, body(nil, item(w.jiraViewer))))[0])) // mgr's
	grantsOf := func(p person, path string) []string {
		t.Helper()
		r := w.get(p.c, path)
		testutil.RequireStatus(t, r, http.StatusOK, "")
		var out []string
		for _, g := range r.Body["grants"].([]any) {
			out = append(out, g.(map[string]any)["employee"].(map[string]any)["id"].(string))
		}
		return out
	}
	for _, tc := range []struct {
		who  person
		path string
		want []string
	}{
		{w.dev, "/api/v1/access", []string{w.dev.ID.String()}},
		{w.mgr, "/api/v1/access", []string{w.mgr.ID.String()}},
		{w.mgr, "/api/v1/access/team/" + w.dev.ID.String(), []string{w.dev.ID.String()}}, // a direct report
		{w.skip, "/api/v1/access/team/" + w.mgr.ID.String(), []string{w.mgr.ID.String()}},
		{w.admin, "/api/v1/access", nil},
	} {
		if got := grantsOf(tc.who, tc.path); len(got) != len(tc.want) || (len(got) == 1 && got[0] != tc.want[0]) {
			t.Errorf("%s: %v; want %v", tc.path, got, tc.want)
		}
	}
	// Direct reports only: not a skip-level report, not one's manager.
	testutil.RequireStatus(t, w.get(w.skip.c, "/api/v1/access/team/"+w.dev.ID.String()), http.StatusNotFound, "not_found")
	testutil.RequireStatus(t, w.get(w.dev.c, "/api/v1/access/team/"+w.mgr.ID.String()), http.StatusNotFound, "not_found")
	g := w.myGrant(w.dev.c, w.jiraViewer)
	if g["tool"] != "Jira" || g["level"] != "Viewer" || g["method"] != "API" || g["displayStatus"] != "PROVISIONING" {
		t.Fatalf("grant = %v", g)
	}
}

// The removal arrives while a provisioning attempt is in flight, and that attempt fails:
// the GRANT is cancelled (never retried after the removal) and the REVOKE runs.
func TestGrantFailingAfterRevokeIsCancelled(t *testing.T) {
	t.Parallel()
	for _, fail := range []struct {
		name   string
		inject func(*testutil.Faults)
	}{
		{"transient", func(f *testutil.Faults) { f.FailNext(1, http.StatusServiceUnavailable) }},
		{"rejected", func(f *testutil.Faults) { f.RespondNext(http.StatusConflict, `{"error":"level_suspended"}`) }},
	} {
		t.Run(fail.name, func(t *testing.T) {
			w := newWorld(t)
			tl := w.startTools()
			w.approved(w.jiraViewer)
			// The held Apply reaches the tool; the status lookup after it fails.
			done := w.inFlight(tl, fail.inject)
			requireGrant(t, w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.jiraViewer)), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
			if err := done(); err != nil {
				t.Fatal(err)
			}
			if n := w.count("SELECT count(*) FROM operations WHERE type = 'GRANT' AND status = 'CANCELLED'"); n != 1 || w.audits("OPERATION_CANCELLED") != 1 {
				t.Fatalf("GRANT cancelled = %d, OPERATION_CANCELLED = %d", n, w.audits("OPERATION_CANCELLED"))
			}
			tl.drain(t)
			g := w.myGrant(w.dev.c, w.jiraViewer)
			requireGrant(t, g, "REVOKED", "REVOKED / VERIFIED")
			if tl.held(t, g, w.dev.Email) != 0 {
				t.Fatal("the tool still holds the level")
			}
		})
	}
}
