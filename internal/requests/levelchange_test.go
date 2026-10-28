package requests

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// approveAll decides every step of the item by its usual approver and returns the item.
func (w *world) approveAll(it map[string]any) map[string]any {
	w.t.Helper()
	by := map[string]person{"MANAGER": w.mgr, "TOOL_OWNER": w.owner, "INFOSEC": w.is1}
	for it["status"] == "PENDING_APPROVAL" {
		s := open(w.t, it)
		it = w.approve(by[s["type"].(string)].c, s)
	}
	return it
}

// overlaps is the D4 overlap list in Exceptions.
func (w *world) overlaps() []any {
	w.t.Helper()
	r := w.exceptions(w.is1)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	return r.Body["overlaps"].([]any)
}

// levelChange gives dev an ACTIVE Jira Viewer and an approved change to Jira Admin; it
// returns the old grant and the approved item.
func (w *world) levelChange(tl *tools) (map[string]any, map[string]any) {
	w.t.Helper()
	w.approved(w.jiraViewer)
	tl.drain(w.t)
	old := w.myGrant(w.dev.c, w.jiraViewer)
	it := w.approveAll(items(w.mustChange(w.dev.c, old, changeBody(w.jiraAdmin, untilRevoked)))[0])
	if it["status"] != "APPROVED" {
		w.t.Fatalf("level change = %v", it)
	}
	return old, it
}

func (w *world) timeOf(sql string, args ...any) time.Time {
	w.t.Helper()
	var at time.Time
	if err := w.db.App.QueryRow(context.Background(), sql, args...).Scan(&at); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return at
}

func TestLevelChangeAPI(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	old := w.myGrant(w.dev.c, w.jiraViewer)
	emails := w.count("SELECT count(*) FROM email_outbox")

	req := w.mustChange(w.dev.c, old, changeBody(w.jiraAdmin, untilRevoked))
	it := items(req)[0]
	if got := steps(it); req["kind"] != "LEVEL_CHANGE" || len(got) != 3 ||
		got[0]["type"] != "MANAGER" || got[1]["type"] != "TOOL_OWNER" || got[2]["type"] != "INFOSEC" { // D4: the new level's steps
		t.Fatalf("level change = %v", req)
	}
	it = w.approveAll(it)
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "ACTIVE", "ACTIVE / VERIFIED")
	requireGrant(t, w.myGrant(w.dev.c, w.jiraAdmin), "PROVISIONING", "PROVISIONING")
	if w.removals(old) != 0 {
		t.Fatal("the old level's removal exists before the new level is verified (D4)")
	}

	found, err := tl.runner.RunOnce(context.Background()) // the new level's GRANT
	if err != nil || !found {
		t.Fatal(found, err)
	}
	niu := w.myGrant(w.dev.c, w.jiraAdmin)
	requireGrant(t, niu, "ACTIVE", "ACTIVE / VERIFIED")
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
	// A9: the old level's removal is created in the transaction that activated the new one.
	if n := w.count(`SELECT count(*) FROM operations o JOIN grants n ON n.id = $2
		WHERE o.grant_id = $1 AND o.type = 'REVOKE' AND o.reason = 'LEVEL_CHANGE' AND o.priority = 1 AND o.created_at = n.effective_from`,
		old["id"], niu["id"]); n != 1 {
		t.Fatalf("level-change removal in the activation's transaction = %d", n)
	}
	if f := it["fulfillment"].(map[string]any); f["grantId"] != niu["id"] {
		t.Fatalf("fulfillment = %v", f)
	}

	tl.drain(t)
	old = w.myGrant(w.dev.c, w.jiraViewer)
	requireGrant(t, old, "REVOKED", "REVOKED / VERIFIED")
	if tl.held(t, old, w.dev.Email) != 0 || tl.held(t, niu, w.dev.Email) != 1 {
		t.Fatal("the mock must hold only the new level")
	}
	// D4: history keeps both periods, and the old one never ends before the new one began.
	if !w.timeOf("SELECT effective_to FROM grants WHERE id = $1", old["id"]).After(
		w.timeOf("SELECT effective_from FROM grants WHERE id = $1", niu["id"])) {
		t.Fatal("the old level ended before the new one began")
	}
	if w.count("SELECT count(*) FROM email_outbox") != emails {
		t.Fatal("a level change sent an email (O2)")
	}
	if len(w.overlaps()) != 0 {
		t.Fatal("a completed level change is listed as an overlap")
	}
}

func TestLevelChangeManual(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	owner := testutil.CreateLevel(t, w.db, f.manualTool, "Owner", false, false)
	old := w.activeManual(f)
	it := w.approveAll(items(w.mustChange(w.dev.c, old, changeBody(owner, untilRevoked)))[0])
	if it["status"] != "APPROVED" || w.task(f.fixer, f.manual, "REVOKE") != nil {
		t.Fatalf("item %v; the old level's removal must wait for the confirmation", it["status"])
	}
	task := w.mustAct(f.fixer, w.mustTask(f.fixer, owner, "GRANT"), "claim", nil)
	requireGrant(t, w.myGrant(w.dev.c, f.manual), "ACTIVE", "ACTIVE / SELF_REPORTED") // still held while the task is worked
	w.mustAct(f.fixer, task, "confirm", nil)
	requireGrant(t, w.myGrant(w.dev.c, owner), "ACTIVE", "ACTIVE / SELF_REPORTED")
	requireGrant(t, w.myGrant(w.dev.c, f.manual), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
	// The system requests the old level's removal, as for API: the confirmer only confirmed the new level.
	if n := w.count(`SELECT count(*) FROM grants g JOIN audit_events a ON a.entity_id = g.id AND a.action = 'GRANT_REVOCATION_REQUESTED'
		WHERE g.id = $1 AND g.revocation_requested_by IS NULL AND a.actor_id IS NULL`, old["id"]); n != 1 {
		t.Fatalf("system removal of the old level = %d", n)
	}

	removal := w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "REVOKE"), "claim", nil)
	w.mustAct(f.fixer, removal, "fail", map[string]any{"reason": "account locked"})
	if ov := w.overlaps(); len(ov) != 1 || ov[0].(map[string]any)["oldGrantId"] != old["id"] {
		t.Fatalf("overlaps after a failed Manual removal = %v", ov)
	}
	testutil.RequireStatus(t, retry(w.admin, w.failedOp(w.admin)), http.StatusOK, "")
	if len(w.overlaps()) != 1 {
		t.Fatal("the overlap left Exceptions before the removal was confirmed")
	}
	w.mustAct(f.fixer, w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "REVOKE"), "claim", nil), "confirm", nil)
	requireGrant(t, w.myGrant(w.dev.c, f.manual), "REVOKED", "REVOKED / SELF_REPORTED")
	if len(w.overlaps()) != 0 {
		t.Fatal("still listed after the removal was confirmed")
	}
}

func TestLevelChangeNewGrantFails(t *testing.T) {
	t.Run("API", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		old, _ := w.levelChange(tl)
		tl.faults.RespondNext(http.StatusConflict, `{"error":"level_suspended"}`)
		tl.drain(t)
		requireGrant(t, w.myGrant(w.dev.c, w.jiraAdmin), "FAILED", "FAILED")
		requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "ACTIVE", "ACTIVE / VERIFIED")
		if w.removals(old) != 0 || tl.held(t, old, w.dev.Email) != 1 {
			t.Fatal("a failed new level touched the old one (D4)")
		}
		op := w.failedOp(w.admin)
		if op["type"] != "GRANT" || op["level"] != "Admin" {
			t.Fatalf("failed operation = %v", op)
		}
		testutil.RequireStatus(t, retry(w.admin, op), http.StatusOK, "") // retryable (O3)
		tl.drain(t)
		requireGrant(t, w.myGrant(w.dev.c, w.jiraAdmin), "ACTIVE", "ACTIVE / VERIFIED")
		requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "REVOKED", "REVOKED / VERIFIED")
	})
	t.Run("Manual", func(t *testing.T) {
		w := newWorld(t)
		f := w.fulfillers()
		owner := testutil.CreateLevel(t, w.db, f.manualTool, "Owner", false, false)
		old := w.activeManual(f)
		w.approveAll(items(w.mustChange(w.dev.c, old, changeBody(owner, untilRevoked)))[0])
		task := w.mustAct(f.fixer, w.mustTask(f.fixer, owner, "GRANT"), "claim", nil)
		w.mustAct(f.fixer, task, "fail", map[string]any{"reason": "seat limit reached"})
		requireGrant(t, w.myGrant(w.dev.c, owner), "FAILED", "FAILED")
		requireGrant(t, w.myGrant(w.dev.c, f.manual), "ACTIVE", "ACTIVE / SELF_REPORTED")
		if w.removals(old) != 0 {
			t.Fatal("a failed new level touched the old one (D4)")
		}
	})
}

func TestLevelChangeOverlap(t *testing.T) {
	t.Run("the old removal fails, then a retry succeeds", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		old, _ := w.levelChange(tl)
		if _, err := tl.runner.RunOnce(context.Background()); err != nil { // the new level
			t.Fatal(err)
		}
		if len(w.overlaps()) != 0 {
			t.Fatal("a removal still pending normally is listed (O7)")
		}
		tl.faults.FailNext(1, http.StatusServiceUnavailable) // first attempt fails, a retry is due later
		tl.drain(t)
		ov := w.overlaps()
		if len(ov) != 1 {
			t.Fatalf("overlaps after the first failed attempt = %v", ov)
		}
		o := ov[0].(map[string]any)
		if o["tool"] != "Jira" || o["oldLevel"] != "Viewer" || o["newLevel"] != "Admin" || o["oldGrantId"] != old["id"] ||
			o["employee"].(map[string]any)["id"] != w.dev.ID.String() || o["lastError"] == nil {
			t.Fatalf("overlap = %v", o)
		}
		w.exec("UPDATE operations SET next_attempt_at = now() WHERE grant_id = $1 AND type = 'REVOKE'", old["id"])
		tl.drain(t)
		if len(w.overlaps()) != 0 {
			t.Fatal("still listed after the removal was verified")
		}
	})
	t.Run("the old grant revoked by its employee, whatever the reason", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		old, _ := w.levelChange(tl)
		w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.jiraViewer)) // runs first: removals have priority
		tl.faults.RespondNext(http.StatusConflict, `{"error":"locked"}`)
		tl.drain(t)
		requireGrant(t, w.myGrant(w.dev.c, w.jiraAdmin), "ACTIVE", "ACTIVE / VERIFIED")
		if ov := w.overlaps(); len(ov) != 1 || ov[0].(map[string]any)["oldGrantId"] != old["id"] {
			t.Fatalf("overlaps = %v", ov)
		}
		if w.removals(old) != 1 {
			t.Fatalf("removals of the old grant = %d", w.removals(old))
		}
	})
}

func TestLevelChangeRules(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	old := w.myGrant(w.dev.c, w.jiraViewer)
	retired := testutil.CreateLevel(t, w.db, w.jira, "Old", false, false)
	w.exec("UPDATE access_levels SET status = 'RETIRED' WHERE id = $1", retired)

	cases := []struct {
		name   string
		by     person
		b      map[string]any
		status int
		code   string
	}{
		{"another tool's level", w.dev, changeBody(w.awsAdmin, untilRevoked), 400, "level_of_another_tool"},
		{"a retired level", w.dev, changeBody(retired, untilRevoked), 400, "level_not_requestable"},
		{"no such level", w.dev, changeBody(uuid.New(), untilRevoked), 400, "level_not_found"},
		{"someone else's grant", w.owner, changeBody(w.jiraAdmin, untilRevoked), 404, ""},
		{"a second level outside a level change", w.dev, nil, 409, "access_exists"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.b == nil {
				testutil.RequireStatus(t, submit(w.dev.c, body(nil, item(w.jiraAdmin))), tc.status, tc.code)
				return
			}
			testutil.RequireStatus(t, change(tc.by.c, old, tc.b), tc.status, tc.code)
		})
	}

	w.mustChange(w.dev.c, old, changeBody(w.jiraAdmin, untilRevoked))
	testutil.RequireStatus(t, change(w.dev.c, old, changeBody(w.jiraAdmin, untilRevoked)), 409, "open_request_exists")

	approved := items(w.get(w.dev.c, "/api/v1/requests").Body["requests"].([]any)[0].(map[string]any))[0]
	w.approveAll(approved) // the new grant is on its way: no other change of the old one meanwhile
	testutil.RequireStatus(t, change(w.dev.c, old, changeBody(w.jiraViewer, untilRevoked)), 409, "change_in_progress")

	_, err := w.db.App.Exec(context.Background(), `INSERT INTO grants (request_item_id, employee_id, tool_id, access_level_id, method, status, replaces_grant_id)
		SELECT i.id, i.beneficiary_id, i.tool_id, $2, 'API', 'PROVISIONING', $3 FROM request_items i WHERE i.id = $1`,
		items(w.mustSubmit(w.dev.c, body(nil, item(w.github))))[0]["id"], w.github, old["id"])
	testutil.RequireSQLState(t, err, "23505") // one open replacement per grant
}

func TestLevelChangeAfterOldGrantEnded(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	old := w.myGrant(w.dev.c, w.jiraViewer)
	it := items(w.mustChange(w.dev.c, old, changeBody(w.jiraAdmin, untilRevoked)))[0]
	w.mustRevoke(w.dev.c, old)
	closed := items(w.get(w.dev.c, "/api/v1/requests/"+it["requestId"].(string)).Body)[0]
	if closed["status"] != "CANCELLED" || closed["closedReason"] != "the access has ended" {
		t.Fatalf("level change after the old grant ended = %v", closed)
	}
	if w.count("SELECT count(*) FROM grants WHERE access_level_id = $1", w.jiraAdmin) != 0 {
		t.Fatal("a new level was created for ended access")
	}
}

// The old grant revoked while the new one provisions is the normal removal: the new grant
// still activates, and no second removal of the old one is created.
func TestOldGrantRemovedDuringChange(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	old, _ := w.levelChange(tl)
	w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.jiraViewer))
	tl.drain(t)
	requireGrant(t, w.myGrant(w.dev.c, w.jiraAdmin), "ACTIVE", "ACTIVE / VERIFIED")
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "REVOKED", "REVOKED / VERIFIED")
	if w.removals(old) != 1 {
		t.Fatalf("removals of the old grant = %d", w.removals(old))
	}
}

// The new level revoked while its GRANT is in flight never becomes ACTIVE: the old level,
// still the one in use, is kept (D4).
func TestNewLevelRevokedInFlightKeepsTheOld(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	old, _ := w.levelChange(tl)
	tl.faults.DelayNext(500 * time.Millisecond)
	done := make(chan error, 1)
	go func() { _, err := tl.runner.RunOnce(context.Background()); done <- err }()
	for deadline := time.Now().Add(5 * time.Second); w.count("SELECT count(*) FROM operations WHERE status = 'IN_PROGRESS'") == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("never claimed")
		}
	}
	w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.jiraAdmin))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	tl.drain(t)
	requireGrant(t, w.myGrant(w.dev.c, w.jiraAdmin), "REVOKED", "REVOKED / VERIFIED")
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "ACTIVE", "ACTIVE / VERIFIED")
	if w.removals(old) != 0 {
		t.Fatal("the old level was removed for a new level that never became ACTIVE")
	}
}
