package requests

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// approved submits level for dev and lets mgr approve its only step; it returns the item.
func (w *world) approved(level uuid.UUID) map[string]any {
	w.t.Helper()
	req := w.mustSubmit(w.dev.c, body(nil, item(level)))
	return w.approve(w.mgr.c, open(w.t, items(req)[0]))
}

func TestFinalApprovalCreatesGrantAndOperation(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	it := w.approved(w.jiraViewer)
	if it["status"] != "APPROVED" {
		t.Fatalf("item = %v", it["status"])
	}
	if n := w.count(`SELECT count(*) FROM grants g JOIN operations o ON o.grant_id = g.id
		WHERE g.request_item_id = $1 AND g.employee_id = $2 AND g.tool_id = $3 AND g.access_level_id = $4
		  AND g.method = 'API' AND g.status = 'PROVISIONING' AND g.activation_evidence IS NULL AND g.effective_from IS NULL
		  AND o.type = 'GRANT' AND o.priority = 2 AND o.status = 'PENDING' AND o.attempts = 0`,
		it["id"], w.dev.ID, w.jira, w.jiraViewer); n != 1 {
		t.Fatalf("grant with its operation = %d", n)
	}
	// One transaction: the grant, its event and the approval share the transaction time.
	if n := w.count(`SELECT count(*) FROM grants g JOIN request_items i ON i.id = g.request_item_id
		JOIN audit_events a ON a.action = 'GRANT_CREATED' AND a.entity_id = g.id AND a.subject_employee_id = g.employee_id
		JOIN audit_events b ON b.action = 'ITEM_APPROVED' AND b.entity_id = i.id
		WHERE g.created_at = i.closed_at AND a.occurred_at = i.closed_at AND b.occurred_at = i.closed_at`); n != 1 {
		t.Fatalf("grant, GRANT_CREATED and ITEM_APPROVED in one transaction = %d", n)
	}
}

func TestTemporaryGrantKeepsTheExpiryDate(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tomorrow := time.Now().AddDate(0, 0, 2).Format("2006-01-02")
	temp := item(w.jiraViewer)
	temp["duration"] = map[string]any{"type": "TEMPORARY", "expiresOn": tomorrow}
	req := w.mustSubmit(w.dev.c, body(nil, temp))
	w.approve(w.mgr.c, open(t, items(req)[0]))
	if n := w.count("SELECT count(*) FROM grants WHERE to_char(expires_on, 'YYYY-MM-DD') = $1", tomorrow); n != 1 {
		t.Fatalf("grant expiring %s = %d", tomorrow, n)
	}
}

func TestManualToolGrantWaitsForFulfillment(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, method := range []string{"MANUAL", "VENDOR"} {
		tool := testutil.CreateTool(t, w.db, testutil.ToolSpec{Method: method, OwnerID: w.owner.ID})
		w.approved(testutil.CreateLevel(t, w.db, tool, "User", false, false))
		if n := w.count("SELECT count(*) FROM grants WHERE tool_id = $1 AND method = $2 AND status = 'PENDING_FULFILLMENT'", tool, method); n != 1 {
			t.Fatalf("%s grant pending fulfillment = %d", method, n)
		}
	}
}

func TestStricterWinsAddsAStepBeforeProvisioning(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	req := w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer)))
	w.exec("UPDATE access_levels SET requires_tool_owner = true, requires_infosec = true WHERE id = $1", w.jiraViewer)

	it := w.approve(w.mgr.c, open(t, items(req)[0]))
	got := steps(it)
	if it["status"] != "PENDING_APPROVAL" || len(got) != 3 ||
		got[1]["type"] != "TOOL_OWNER" || got[1]["status"] != "PENDING" || got[1]["assigneeId"] != w.owner.ID.String() ||
		got[2]["type"] != "INFOSEC" || got[2]["status"] != "WAITING" {
		t.Fatalf("after the manager: %v %v", it["status"], got)
	}
	if n := w.audits("STEP_ADDED_BY_POLICY"); n != 2 {
		t.Fatalf("STEP_ADDED_BY_POLICY = %d", n)
	}
	if n := w.count("SELECT count(*) FROM grants"); n != 0 {
		t.Fatalf("grants before the added steps = %d", n)
	}
	it = w.approve(w.owner.c, open(t, it))
	it = w.approve(w.is1.c, open(t, it))
	if it["status"] != "APPROVED" || w.count("SELECT count(*) FROM grants") != 1 {
		t.Fatalf("after every step: %v", it["status"])
	}
}

func TestRemovingAStepNeverRemovesOne(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	req := w.mustSubmit(w.dev.c, body(nil, item(w.github))) // Manager → Tool Owner
	w.exec("UPDATE access_levels SET requires_tool_owner = false WHERE id = $1", w.github)
	it := w.approve(w.mgr.c, open(t, items(req)[0]))
	if s := open(t, it); s["type"] != "TOOL_OWNER" || w.audits("STEP_ADDED_BY_POLICY") != 0 {
		t.Fatalf("open step = %v", s["type"])
	}
}

func TestRetiredLevelIsNeverProvisioned(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, sql, reason string }{
		{"level", "UPDATE access_levels SET status = 'RETIRED' WHERE tool_id = $1", "access level retired"},
		{"tool", "UPDATE tools SET status = 'RETIRED' WHERE id = $1", "tool retired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			req := w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer)))
			w.exec(tc.sql, w.jira)
			it := w.approve(w.mgr.c, open(t, items(req)[0]))
			if it["status"] != "CANCELLED" || it["closedReason"] != tc.reason {
				t.Fatalf("item = %v (%v)", it["status"], it["closedReason"])
			}
			if n := w.count("SELECT count(*) FROM grants") + w.count("SELECT count(*) FROM operations"); n != 0 {
				t.Fatalf("grants and operations = %d", n)
			}
			if n := w.count("SELECT count(*) FROM audit_events WHERE action = 'ITEM_CANCELLED' AND reason = $1 AND entity_id = $2",
				tc.reason, it["id"]); n != 1 {
				t.Fatalf("ITEM_CANCELLED = %d", n)
			}
			if n := w.audits("ITEM_APPROVED"); n != 0 {
				t.Fatalf("ITEM_APPROVED = %d", n)
			}
		})
	}
}

func TestNoSecondRequestWhileAccessIsOpen(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.approved(w.jiraViewer)
	conflict := func(status string) {
		t.Helper()
		w.exec("UPDATE grants SET status = $1", status)
		for _, level := range []uuid.UUID{w.jiraViewer, w.jiraAdmin} {
			testutil.RequireStatus(t, submit(w.dev.c, body(nil, item(level))), http.StatusConflict, "access_exists")
		}
	}
	for _, status := range []string{"PROVISIONING", "ACTIVE", "FAILED", "REVOCATION_REQUESTED", "REVOCATION_FAILED"} {
		conflict(status)
	}
	r := submit(w.dev.c, body(nil, item(w.jiraViewer)))
	if r.Body["field"] != "items[0].accessLevelId" {
		t.Errorf("field = %v", r.Body["field"])
	}
	for _, status := range []string{"REVOKED", "CANCELLED"} {
		w.exec("UPDATE grants SET status = $1", status)
		req := w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer)))
		it := items(req)[0]
		testutil.RequireStatus(t, w.dev.c.Do(http.MethodPost, "/api/v1/request-items/"+it["id"].(string)+"/withdraw",
			map[string]any{"version": it["version"], "reason": "test"}), http.StatusOK, "")
	}
}

// A grant committed while the submission waited for the beneficiary's lock is seen.
func TestSubmitSeesAGrantCommittedWhileItWaited(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	slack := testutil.CreateTool(t, w.db, testutil.ToolSpec{OwnerID: w.owner.ID})
	w.approved(testutil.CreateLevel(t, w.db, slack, "Member", false, false)) // dev's grant row; setup moves it to Jira
	if n := w.count("SELECT count(*) FROM grants WHERE employee_id = $1", w.dev.ID); n != 1 {
		t.Fatalf("grants = %d", n)
	}
	var r testutil.Response
	err := testutil.WhileLocked(t, w.db.App, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", w.dev.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE grants SET tool_id = $2, access_level_id = $3 WHERE employee_id = $1`, w.dev.ID, w.jira, w.jiraViewer)
		return err
	}, func() error {
		r = submit(w.dev.c, body(nil, item(w.jiraAdmin)))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	testutil.RequireStatus(t, r, http.StatusConflict, "access_exists")
}

func TestRequestShowsFulfillment(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	req := w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer)))
	path := "/api/v1/requests/" + req["id"].(string)
	if f := items(w.get(w.dev.c, path).Body)[0]["fulfillment"]; f != nil {
		t.Fatalf("fulfillment before approval = %v", f)
	}
	w.approve(w.mgr.c, open(t, items(req)[0]))
	var grant string
	if err := w.db.App.QueryRow(context.Background(), "SELECT id FROM grants").Scan(&grant); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sql, status string
		evidence    any
	}{
		{"UPDATE grants SET version = version", "PROVISIONING", nil},
		{"UPDATE grants SET status = 'ACTIVE', activation_evidence = 'VERIFIED', effective_from = now()", "ACTIVE / VERIFIED", "VERIFIED"},
	} {
		w.exec(tc.sql)
		f, _ := items(w.get(w.dev.c, path).Body)[0]["fulfillment"].(map[string]any)
		if f["grantId"] != grant || f["status"] != tc.status || f["evidence"] != tc.evidence {
			t.Fatalf("fulfillment = %v; want %s", f, tc.status)
		}
	}
}

// Retired after approval: the runner cancels the grant, and My Requests shows why.
func TestRetiredAfterApprovalShowsTheReason(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	req := w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer)))
	w.approve(w.mgr.c, open(t, items(req)[0]))
	w.exec("UPDATE access_levels SET status = 'RETIRED' WHERE id = $1", w.jiraViewer)
	tl.drain(t)
	f := items(w.get(w.dev.c, "/api/v1/requests/"+req["id"].(string)).Body)[0]["fulfillment"].(map[string]any)
	if f["status"] != "CANCELLED" || f["closedReason"] != "access level retired" || tl.faults.Requests() != 0 {
		t.Fatalf("fulfillment = %v, tool requests %d", f, tl.faults.Requests())
	}
}

// Retired and made stricter: the item is cancelled at once; nobody is asked to approve it.
func TestRetiredIsCheckedBeforeStricterSteps(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	req := w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer)))
	w.exec("UPDATE access_levels SET requires_infosec = true, status = 'RETIRED' WHERE id = $1", w.jiraViewer)
	it := w.approve(w.mgr.c, open(t, items(req)[0]))
	if it["status"] != "CANCELLED" || it["closedReason"] != "access level retired" || len(steps(it)) != 1 {
		t.Fatalf("item = %v (%v), steps %d", it["status"], it["closedReason"], len(steps(it)))
	}
	if n := w.audits("STEP_ADDED_BY_POLICY"); n != 0 {
		t.Fatalf("STEP_ADDED_BY_POLICY = %d", n)
	}
}

// Regression (ENG-003): while someone else holds the employee, a retired grant cannot be
// cancelled yet; the runner must report it idle and move it back, so it pauses instead of
// reclaiming it at once.
func TestRunnerPausesWhileARetiredGrantsEmployeeIsBusy(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	req := w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer)))
	w.approve(w.mgr.c, open(t, items(req)[0]))
	w.exec("UPDATE access_levels SET status = 'RETIRED' WHERE id = $1", w.jiraViewer)
	ctx := context.Background()
	tx, err := w.db.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", w.dev.ID); err != nil {
		t.Fatal(err)
	}
	if found, err := tl.runner.RunOnce(ctx); err != nil || found {
		t.Fatalf("RunOnce = %v, %v; want idle while the employee is busy", found, err)
	}
	tx.Rollback(ctx)
	// The skipped operation was moved back a few seconds (D12: tests make it due, not wait).
	w.exec("UPDATE operations SET next_attempt_at = now() WHERE grant_id = (SELECT id FROM grants WHERE request_item_id = $1)", items(req)[0]["id"])
	tl.drain(t)
	f := items(w.get(w.dev.c, "/api/v1/requests/"+req["id"].(string)).Body)[0]["fulfillment"].(map[string]any)
	if f["status"] != "CANCELLED" {
		t.Fatalf("fulfillment = %v", f)
	}
}
