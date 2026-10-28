package requests

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// fulfillers adds a Manual and a Vendor tool, each with a level only the manager approves,
// and fixer and fixer2, members of both tools' admin groups.
type fulfillers struct {
	manualTool, vendorTool uuid.UUID
	manual, vendor         uuid.UUID // levels
	fixer, fixer2          person
}

func (w *world) fulfillers() fulfillers {
	w.t.Helper()
	f := fulfillers{
		manualTool: testutil.CreateTool(w.t, w.db, testutil.ToolSpec{Name: "Figma", Method: "MANUAL", OwnerID: w.owner.ID}),
		vendorTool: testutil.CreateTool(w.t, w.db, testutil.ToolSpec{Name: "Salesforce", Method: "VENDOR", OwnerID: w.owner.ID}),
		fixer:      w.person(testutil.EmployeeSpec{}),
		fixer2:     w.person(testutil.EmployeeSpec{}),
	}
	f.manual = testutil.CreateLevel(w.t, w.db, f.manualTool, "Editor", false, false)
	f.vendor = testutil.CreateLevel(w.t, w.db, f.vendorTool, "User", false, false)
	for _, p := range []person{f.fixer, f.fixer2} {
		w.member(p, f.manualTool)
		w.member(p, f.vendorTool)
	}
	return f
}

func (w *world) taskList(p person) []map[string]any {
	w.t.Helper()
	r := w.get(p.c, "/api/v1/fulfillment-tasks")
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	var out []map[string]any
	for _, t := range r.Body["tasks"].([]any) {
		out = append(out, t.(map[string]any))
	}
	return out
}

// task is p's queued task of typ for dev's grant of level, or nil.
func (w *world) task(p person, level uuid.UUID, typ string) map[string]any {
	w.t.Helper()
	for _, t := range w.taskList(p) {
		if t["accessLevelId"] == level.String() && t["type"] == typ {
			return t
		}
	}
	return nil
}

func (w *world) mustTask(p person, level uuid.UUID, typ string) map[string]any {
	w.t.Helper()
	t := w.task(p, level, typ)
	if t == nil {
		w.t.Fatalf("no %s task for %v in %v", typ, level, w.taskList(p))
	}
	return t
}

// act posts action (claim, ticket, confirm, fail) on task with its version.
func act(p person, task map[string]any, action string, extra map[string]any) testutil.Response {
	b := map[string]any{"version": task["version"]}
	for k, v := range extra {
		b[k] = v
	}
	return p.c.Do(http.MethodPost, "/api/v1/fulfillment-tasks/"+task["id"].(string)+"/"+action, b)
}

// mustAct posts action and returns the task.
func (w *world) mustAct(p person, task map[string]any, action string, extra map[string]any) map[string]any {
	w.t.Helper()
	r := act(p, task, action, extra)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	return r.Body
}

func TestManualTaskEndToEnd(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	it := w.approved(f.manual)
	requireGrant(t, w.myGrant(w.dev.c, f.manual), "PENDING_FULFILLMENT", "PENDING_FULFILLMENT")

	task := w.mustTask(f.fixer, f.manual, "GRANT")
	if task["status"] != "PENDING" || task["method"] != "MANUAL" || task["tool"] != "Figma" || task["level"] != "Editor" ||
		task["itemId"] != it["id"] || task["requestId"] != it["requestId"] || task["group"] == "" || task["canAct"] != true ||
		task["employee"].(map[string]any)["id"] != w.dev.ID.String() || task["claimedBy"] != nil || task["createdAt"] == nil {
		t.Fatalf("task = %v", task)
	}
	if w.task(w.mgr, f.manual, "GRANT") != nil {
		t.Fatal("a non-member sees the task")
	}

	task = w.mustAct(f.fixer, task, "claim", nil)
	if task["status"] != "IN_PROGRESS" || task["claimedBy"].(map[string]any)["id"] != f.fixer.ID.String() {
		t.Fatalf("claimed task = %v", task)
	}
	testutil.RequireStatus(t, act(f.fixer, task, "ticket", map[string]any{"ticketRef": "T-1"}), http.StatusConflict, "not_a_vendor_task")
	w.mustAct(f.fixer, task, "confirm", nil)

	g := w.myGrant(w.dev.c, f.manual)
	requireGrant(t, g, "ACTIVE", "ACTIVE / SELF_REPORTED")
	if g["effectiveFrom"] == nil || g["evidence"] != "SELF_REPORTED" {
		t.Fatalf("grant = %v", g)
	}
	if n := w.count(`SELECT count(*) FROM operations o JOIN audit_events a ON a.action = 'GRANT_ACTIVATED' AND a.entity_id = o.grant_id
		WHERE o.id = $1 AND o.status = 'SUCCEEDED' AND o.completed_at IS NOT NULL AND o.claimed_by = $2 AND a.actor_id = $2
		  AND a.details->>'evidence' = 'SELF_REPORTED'`, task["id"], f.fixer.ID); n != 1 {
		t.Fatalf("confirmed by fixer = %d", n)
	}
	if w.audits("TASK_CLAIMED") != 1 || w.task(f.fixer, f.manual, "GRANT") != nil {
		t.Fatal("the task must be claimed once and leave the queue")
	}
	if fl := items(w.get(w.dev.c, "/api/v1/requests").Body["requests"].([]any)[0].(map[string]any))[0]["fulfillment"].(map[string]any); fl["status"] != "ACTIVE / SELF_REPORTED" {
		t.Fatalf("item fulfillment = %v", fl)
	}
	// A9: a Manual result is never verified, not even by a direct write.
	_, err := w.db.App.Exec(context.Background(), "UPDATE grants SET activation_evidence = 'VERIFIED' WHERE id = $1", g["id"])
	testutil.RequireSQLState(t, err, "23514")
}

func TestVendorTaskNeedsATicket(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	w.approved(f.vendor)
	task := w.mustAct(f.fixer, w.mustTask(f.fixer, f.vendor, "GRANT"), "claim", nil)

	testutil.RequireStatus(t, act(f.fixer, task, "confirm", nil), http.StatusConflict, "ticket_required")
	testutil.RequireStatus(t, act(f.fixer, task, "ticket", map[string]any{"ticketRef": "  "}), http.StatusBadRequest, "")
	task = w.mustAct(f.fixer, task, "ticket", map[string]any{"ticketRef": " VND-123 "})
	if task["ticketRef"] != "VND-123" {
		t.Fatalf("ticket = %v", task["ticketRef"])
	}
	requireGrant(t, w.myGrant(w.dev.c, f.vendor), "PENDING_FULFILLMENT", "WAITING_VENDOR")
	if fl := items(w.get(w.dev.c, "/api/v1/requests").Body["requests"].([]any)[0].(map[string]any))[0]["fulfillment"].(map[string]any); fl["status"] != "WAITING_VENDOR" {
		t.Fatalf("item fulfillment = %v", fl)
	}

	w.mustAct(f.fixer, task, "confirm", nil)
	requireGrant(t, w.myGrant(w.dev.c, f.vendor), "ACTIVE", "ACTIVE / SELF_REPORTED")
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'GRANT_ACTIVATED' AND details->>'ticketRef' = 'VND-123'
		AND details->>'evidence' = 'SELF_REPORTED'`); n != 1 || w.audits("TICKET_RECORDED") != 1 {
		t.Fatalf("activation names the ticket = %d", n)
	}
}

func TestTaskFailureAndRetry(t *testing.T) {
	for _, vendor := range []bool{false, true} {
		w := newWorld(t)
		f := w.fulfillers()
		level := f.manual
		if vendor {
			level = f.vendor
		}
		w.approved(level)
		task := w.mustAct(f.fixer, w.mustTask(f.fixer, level, "GRANT"), "claim", nil)
		if vendor {
			task = w.mustAct(f.fixer, task, "ticket", map[string]any{"ticketRef": "VND-1"})
		}
		testutil.RequireStatus(t, act(f.fixer, task, "fail", map[string]any{"reason": " "}), http.StatusBadRequest, "")
		w.mustAct(f.fixer, task, "fail", map[string]any{"reason": "the seat limit is reached"})
		requireGrant(t, w.myGrant(w.dev.c, level), "FAILED", "FAILED")
		if w.task(f.fixer, level, "GRANT") != nil {
			t.Fatal("a failed task stays in the queue")
		}

		op := w.failedOp(w.admin)
		if op["id"] != task["id"] || op["lastError"] != "the seat limit is reached" || op["failedAt"] == nil {
			t.Fatalf("exception = %v", op)
		}
		if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'OPERATION_FAILED' AND actor_id = $1 AND entity_id = $2`,
			f.fixer.ID, task["id"]); n != 1 {
			t.Fatalf("OPERATION_FAILED by fixer = %d", n)
		}
		testutil.RequireStatus(t, retry(w.admin, op), http.StatusOK, "")
		requireGrant(t, w.myGrant(w.dev.c, level), "PENDING_FULFILLMENT", "PENDING_FULFILLMENT") // never PROVISIONING
		task = w.mustTask(f.fixer2, level, "GRANT")
		if task["status"] != "PENDING" || task["claimedBy"] != nil || task["ticketRef"] != nil {
			t.Fatalf("retried task = %v", task)
		}
		task = w.mustAct(f.fixer2, task, "claim", nil)
		if vendor {
			// The refused ticket is never evidence for the new attempt.
			testutil.RequireStatus(t, act(f.fixer2, task, "confirm", nil), http.StatusConflict, "ticket_required")
			task = w.mustAct(f.fixer2, task, "ticket", map[string]any{"ticketRef": "VND-2"})
		}
		w.mustAct(f.fixer2, task, "confirm", nil)
		requireGrant(t, w.myGrant(w.dev.c, level), "ACTIVE", "ACTIVE / SELF_REPORTED")
	}
}

func TestClaimSkipsRetiredLevel(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	w.approved(f.manual)
	task := w.mustTask(f.fixer, f.manual, "GRANT")
	w.exec("UPDATE access_levels SET status = 'RETIRED' WHERE id = $1", f.manual)
	testutil.RequireStatus(t, act(f.fixer, task, "claim", nil), http.StatusConflict, "level_retired")
	g := w.myGrant(w.dev.c, f.manual)
	requireGrant(t, g, "CANCELLED", "CANCELLED")
	if g["closedReason"] != "access level retired" || w.count("SELECT count(*) FROM operations WHERE id = $1 AND status = 'CANCELLED'", task["id"]) != 1 {
		t.Fatalf("grant = %v", g)
	}

	// Once claimed, the task is finished even if the level is retired afterwards (safe fulfillment).
	w.approved(f.vendor)
	task = w.mustAct(f.fixer, w.mustTask(f.fixer, f.vendor, "GRANT"), "claim", nil)
	w.exec("UPDATE tools SET status = 'RETIRED' WHERE id = $1", f.vendorTool)
	task = w.mustAct(f.fixer, task, "ticket", map[string]any{"ticketRef": "VND-9"})
	w.mustAct(f.fixer, task, "confirm", nil)
	requireGrant(t, w.myGrant(w.dev.c, f.vendor), "ACTIVE", "ACTIVE / SELF_REPORTED")
}

// activeManual gives dev an ACTIVE Manual grant confirmed by fixer.
func (w *world) activeManual(f fulfillers) map[string]any {
	w.t.Helper()
	w.approved(f.manual)
	w.mustAct(f.fixer, w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "GRANT"), "claim", nil), "confirm", nil)
	return w.myGrant(w.dev.c, f.manual)
}

func TestManualRemovalTask(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	g := w.mustRevoke(w.dev.c, w.activeManual(f))
	requireGrant(t, g, "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")

	task := w.mustTask(f.fixer2, f.manual, "REVOKE")
	w.mustAct(f.fixer2, w.mustAct(f.fixer2, task, "claim", nil), "confirm", nil)
	g = w.myGrant(w.dev.c, f.manual)
	requireGrant(t, g, "REVOKED", "REVOKED / SELF_REPORTED")
	if g["effectiveTo"] == nil {
		t.Fatalf("grant = %v", g)
	}
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'GRANT_REVOKED' AND actor_id = $1
		AND details->>'evidence' = 'SELF_REPORTED'`, f.fixer2.ID); n != 1 {
		t.Fatalf("GRANT_REVOKED by fixer2 = %d", n)
	}
}

func TestRevokeWhileGrantTaskClaimed(t *testing.T) {
	for _, outcome := range []string{"confirm", "fail"} {
		w := newWorld(t)
		f := w.fulfillers()
		w.approved(f.manual)
		grantTask := w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "GRANT"), "claim", nil)
		g := w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, f.manual))
		requireGrant(t, g, "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")

		if w.task(f.fixer2, f.manual, "REVOKE") != nil {
			t.Fatal("the removal task is listed while the grant task is claimed")
		}
		var id string
		var version int
		if err := w.db.App.QueryRow(context.Background(), "SELECT id::text, version FROM operations WHERE grant_id = $1 AND type = 'REVOKE'",
			g["id"]).Scan(&id, &version); err != nil {
			t.Fatal(err)
		}
		testutil.RequireStatus(t, act(f.fixer2, map[string]any{"id": id, "version": version}, "claim", nil), http.StatusConflict, "task_not_ready")

		extra := map[string]any(nil)
		if outcome == "fail" {
			extra = map[string]any{"reason": "could not finish"}
		}
		w.mustAct(f.fixer, grantTask, outcome, extra)
		g = w.myGrant(w.dev.c, f.manual)
		requireGrant(t, g, "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
		wantOp, wantFrom := "SUCCEEDED", true
		if outcome == "fail" {
			wantOp, wantFrom = "CANCELLED", false // the removal takes over
		}
		if n := w.count("SELECT count(*) FROM operations WHERE id = $1 AND status = $2", grantTask["id"], wantOp); n != 1 || (g["effectiveFrom"] != nil) != wantFrom {
			t.Fatalf("%s: grant task %s = %d, grant %v", outcome, wantOp, n, g)
		}
		task := w.mustTask(f.fixer2, f.manual, "REVOKE")
		w.mustAct(f.fixer2, w.mustAct(f.fixer2, task, "claim", nil), "confirm", nil)
		requireGrant(t, w.myGrant(w.dev.c, f.manual), "REVOKED", "REVOKED / SELF_REPORTED")
	}
}

func TestTaskPermissions(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	auditor := w.person(testutil.EmployeeSpec{Roles: []string{"AUDITOR"}})
	w.member(w.dev, f.manualTool)
	w.approved(f.manual)
	task := w.mustTask(f.fixer, f.manual, "GRANT")

	for _, p := range []person{w.mgr, w.admin, auditor} {
		testutil.RequireStatus(t, act(p, task, "claim", nil), http.StatusForbidden, "")
	}
	// The beneficiary sees the work in their group's queue but may not do it (F2).
	if own := w.mustTask(w.dev, f.manual, "GRANT"); own["canAct"] != false {
		t.Fatalf("own task = %v", own)
	}
	testutil.RequireStatus(t, act(w.dev, task, "claim", nil), http.StatusForbidden, "separation_of_duties")

	claimed := w.mustAct(f.fixer, task, "claim", nil)
	for _, action := range []string{"confirm", "ticket", "fail"} {
		testutil.RequireStatus(t, act(f.fixer2, claimed, action, map[string]any{"reason": "x", "ticketRef": "x"}), http.StatusForbidden, "not_claimer")
	}
	testutil.RequireStatus(t, act(f.fixer2, claimed, "claim", nil), http.StatusConflict, "task_claimed")

	// fixer leaves the group: they can no longer act, and fixer2 takes the task over.
	w.exec("UPDATE admin_group_memberships SET valid_to = now() WHERE employee_id = $1", f.fixer.ID)
	testutil.RequireStatus(t, act(f.fixer, claimed, "confirm", nil), http.StatusForbidden, "")
	taken := w.mustAct(f.fixer2, claimed, "claim", nil)
	if taken["claimedBy"].(map[string]any)["id"] != f.fixer2.ID.String() {
		t.Fatalf("taken over = %v", taken)
	}
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'TASK_CLAIMED' AND actor_id = $1 AND details->>'takeover' = 'true'`,
		f.fixer2.ID); n != 1 {
		t.Fatalf("takeover event = %d", n)
	}
	w.mustAct(f.fixer2, taken, "confirm", nil)
	requireGrant(t, w.myGrant(w.dev.c, f.manual), "ACTIVE", "ACTIVE / SELF_REPORTED")
	testutil.RequireStatus(t, act(f.fixer2, map[string]any{"id": uuid.NewString(), "version": 1}, "claim", nil), http.StatusNotFound, "")
}

func TestTaskConcurrency(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	w.approved(f.manual)
	task := w.mustTask(f.fixer, f.manual, "GRANT")

	race := func(calls ...func() testutil.Response) []testutil.Response {
		out := make([]testutil.Response, len(calls))
		var wg sync.WaitGroup
		for i, call := range calls {
			wg.Go(func() { out[i] = call() })
		}
		wg.Wait()
		return out
	}
	claims := race(func() testutil.Response { return act(f.fixer, task, "claim", nil) },
		func() testutil.Response { return act(f.fixer2, task, "claim", nil) })
	ok := 0
	for _, r := range claims {
		switch {
		case r.Status == http.StatusOK:
			ok++
		case r.Status != http.StatusConflict || r.Body["error"] != "task_claimed":
			t.Fatalf("claim = %d %v", r.Status, r.Body)
		}
	}
	if ok != 1 || w.audits("TASK_CLAIMED") != 1 {
		t.Fatalf("claims won = %d", ok)
	}

	winner := f.fixer
	if claims[1].Status == http.StatusOK {
		winner = f.fixer2
	}
	claimed := w.mustTask(winner, f.manual, "GRANT")
	done := race(func() testutil.Response { return act(winner, claimed, "confirm", nil) },
		func() testutil.Response { return act(winner, claimed, "fail", map[string]any{"reason": "x"}) })
	if (done[0].Status == http.StatusOK) == (done[1].Status == http.StatusOK) {
		t.Fatalf("confirm %d, fail %d; want exactly one", done[0].Status, done[1].Status)
	}
	if n := w.audits("GRANT_ACTIVATED") + w.audits("OPERATION_FAILED"); n != 1 {
		t.Fatalf("results = %d", n)
	}
}

func TestTaskActionsAreIdempotent(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	w.approved(f.vendor)
	task := w.mustTask(f.fixer, f.vendor, "GRANT")
	claimed := w.mustAct(f.fixer, task, "claim", nil)
	w.mustAct(f.fixer, task, "claim", nil) // the repeat, with the old version
	ticketed := w.mustAct(f.fixer, claimed, "ticket", map[string]any{"ticketRef": "VND-1"})
	w.mustAct(f.fixer, claimed, "ticket", map[string]any{"ticketRef": "VND-1"})
	testutil.RequireStatus(t, act(f.fixer, claimed, "ticket", map[string]any{"ticketRef": "VND-2"}), http.StatusConflict, "version_conflict")
	testutil.RequireStatus(t, act(f.fixer, claimed, "confirm", nil), http.StatusConflict, "version_conflict")
	w.mustAct(f.fixer, ticketed, "confirm", nil)
	w.mustAct(f.fixer, ticketed, "confirm", nil)
	testutil.RequireStatus(t, act(f.fixer, ticketed, "fail", map[string]any{"reason": "x"}), http.StatusConflict, "task_closed")
	if w.audits("TASK_CLAIMED") != 1 || w.audits("TICKET_RECORDED") != 1 || w.audits("GRANT_ACTIVATED") != 1 {
		t.Fatal("a repeat must not record a second event")
	}
}

func TestWithdrawApprovedBeforeWorkStarts(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	f := w.fulfillers()
	for _, level := range []uuid.UUID{f.manual, f.vendor, w.jiraViewer} {
		// skip submits for dev and is not the approver: the submitter may withdraw too.
		for _, by := range []person{w.dev, w.skip} {
			it := w.approve(w.mgr.c, open(t, items(w.mustSubmit(by.c, body(&w.dev.ID, item(level))))[0]))
			r := withdraw(by.c, it, "plans changed")
			testutil.RequireStatus(t, r, http.StatusOK, "")
			if r.Body["status"] != "WITHDRAWN" || r.Body["fulfillment"].(map[string]any)["status"] != "CANCELLED" {
				t.Fatalf("withdrawn item = %v", r.Body)
			}
			if n := w.count(`SELECT count(*) FROM grants g JOIN operations o ON o.grant_id = g.id
				WHERE g.request_item_id = $1 AND g.status = 'CANCELLED' AND g.closed_reason = 'plans changed' AND o.status = 'CANCELLED'`,
				it["id"]); n != 1 {
				t.Fatalf("cancelled grant and operation = %d", n)
			}
			if w.task(f.fixer, level, "GRANT") != nil {
				t.Fatal("a withdrawn item's task stays in the queue")
			}
		}
	}
	tl.drain(t)
	if n := tl.faults.Requests(); n != 0 {
		t.Fatalf("the Mock Tool Service was called %d times", n)
	}
	if w.audits("ITEM_WITHDRAWN") != 6 || w.audits("GRANT_CANCELLED") != 6 {
		t.Fatal("each withdrawal records the item and the grant")
	}
	w.mustSubmit(w.dev.c, body(nil, item(f.manual))) // the access may be requested again
}

// An item approved before M6 has no grant: withdrawing it closes the item, with nothing to cancel.
func TestWithdrawAnItemApprovedWithoutAGrant(t *testing.T) {
	w := newWorld(t)
	it := items(w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer))))[0]
	w.exec(`UPDATE approval_steps SET status = 'APPROVED', decided_by = $2, decided_at = now(), version = version + 1
		WHERE item_id = $1`, it["id"], w.mgr.ID)
	w.exec("UPDATE request_items SET status = 'APPROVED', closed_at = now(), version = version + 1 WHERE id = $1", it["id"])
	it = items(w.get(w.dev.c, "/api/v1/requests").Body["requests"].([]any)[0].(map[string]any))[0]

	r := withdraw(w.dev.c, it, "not needed")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["status"] != "WITHDRAWN" || r.Body["fulfillment"] != nil {
		t.Fatalf("withdrawn item = %v", r.Body)
	}
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'ITEM_WITHDRAWN' AND entity_id = $1 AND actor_id = $2
		AND reason = 'not needed'`, it["id"], w.dev.ID); n != 1 || w.audits("GRANT_CANCELLED") != 0 {
		t.Fatalf("ITEM_WITHDRAWN = %d, GRANT_CANCELLED = %d", n, w.audits("GRANT_CANCELLED"))
	}
	testutil.RequireStatus(t, withdraw(w.dev.c, it, "again"), http.StatusOK, "") // a repeat returns the current state
}

// An approved item whose grant was already cancelled (retired before provisioning) can
// still be withdrawn: no work ever started.
func TestWithdrawAfterTheGrantWasCancelled(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	it := w.approved(f.vendor)
	w.exec("UPDATE access_levels SET status = 'RETIRED' WHERE id = $1", f.vendor)
	testutil.RequireStatus(t, act(f.fixer, w.mustTask(f.fixer, f.vendor, "GRANT"), "claim", nil), http.StatusConflict, "level_retired")
	r := withdraw(w.dev.c, it, "not needed")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["status"] != "WITHDRAWN" || w.audits("GRANT_CANCELLED") != 1 {
		t.Fatalf("withdrawn item = %v", r.Body)
	}
}

func TestWithdrawAfterWorkStarted(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	f := w.fulfillers()
	manual := w.approved(f.manual)
	w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "GRANT"), "claim", nil)
	api := w.approved(w.jiraViewer)
	tl.drain(t)
	for _, it := range []map[string]any{manual, api} {
		testutil.RequireStatus(t, withdraw(w.dev.c, it, "x"), http.StatusConflict, "fulfillment_started")
	}
	requireGrant(t, w.myGrant(w.dev.c, f.manual), "PENDING_FULFILLMENT", "PENDING_FULFILLMENT")
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "ACTIVE", "ACTIVE / VERIFIED")
	if w.audits("ITEM_WITHDRAWN") != 0 {
		t.Fatal("nothing may change once work started")
	}
}

func TestWithdrawRacingClaim(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	it := w.approved(f.manual)
	task := w.mustTask(f.fixer, f.manual, "GRANT")
	// The claim holds the task while the withdrawal arrives: the claim wins.
	var r testutil.Response
	err := testutil.WhileLocked(t, w.db.App, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE operations SET status = 'IN_PROGRESS', claimed_by = $2, first_attempt_at = now(),
			version = version + 1 WHERE id = $1`, task["id"], f.fixer.ID)
		return err
	}, func() error { r = withdraw(w.dev.c, it, "x"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	testutil.RequireStatus(t, r, http.StatusConflict, "fulfillment_started")

	// The withdrawal comes first: the claim finds the task closed.
	it = w.approved(f.vendor)
	task = w.mustTask(f.fixer, f.vendor, "GRANT")
	testutil.RequireStatus(t, withdraw(w.dev.c, it, "x"), http.StatusOK, "")
	testutil.RequireStatus(t, act(f.fixer, task, "claim", nil), http.StatusConflict, "task_closed")
}
