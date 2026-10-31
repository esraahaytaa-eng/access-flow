package requests

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// in returns the company-timezone date days from today.
func in(days int) string {
	istanbul, _ := time.LoadLocation("Europe/Istanbul")
	return time.Now().In(istanbul).AddDate(0, 0, days).Format(time.DateOnly)
}

func directBody(beneficiary, level uuid.UUID, expiresOn any) map[string]any {
	b := map[string]any{"submissionId": uuid.New(), "beneficiaryId": beneficiary, "accessLevelId": level,
		"justification": "incident 42 needs it now"}
	if expiresOn != nil {
		b["expiresOn"] = expiresOn
	}
	return b
}

func directGrant(p person, b map[string]any) testutil.Response {
	return p.c.Do(http.MethodPost, "/api/v1/direct-grants", b)
}

func (w *world) mustDirectGrant(level uuid.UUID) map[string]any {
	w.t.Helper()
	r := directGrant(w.admin, directBody(w.dev.ID, level, in(30)))
	testutil.RequireStatus(w.t, r, http.StatusCreated, "")
	return r.Body
}

func TestDirectGrantAPI(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	b := directBody(w.dev.ID, w.jiraViewer, in(30))
	r := directGrant(w.admin, b)
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	it := items(r.Body)[0]
	if r.Body["kind"] != "DIRECT_GRANT" || it["status"] != "APPROVED" || len(it["steps"].([]any)) != 0 ||
		it["duration"].(map[string]any)["type"] != "TEMPORARY" || it["duration"].(map[string]any)["expiresOn"] != in(30) {
		t.Fatalf("direct grant = %v", r.Body)
	}
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "PROVISIONING", "PROVISIONING")
	tl.drain(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	requireGrant(t, g, "ACTIVE", "ACTIVE / VERIFIED")
	if g["expiresOn"] != in(30) {
		t.Fatalf("grant = %v", g)
	}
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'DIRECT_GRANT_CREATED' AND actor_id = $1 AND subject_employee_id = $2
		AND reason = 'incident 42 needs it now'`, w.admin.ID, w.dev.ID); n != 1 {
		t.Fatalf("DIRECT_GRANT_CREATED = %d", n)
	}

	again := directGrant(w.admin, b) // A9: the same submission returns the original
	testutil.RequireStatus(t, again, http.StatusOK, "")
	if again.Body["id"] != r.Body["id"] || w.count("SELECT count(*) FROM grants") != 1 {
		t.Fatalf("repeat = %v", again.Body)
	}
}

func TestDirectGrantManualInitiatorCannotConfirm(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.fulfillers()
	w.member(w.admin, f.manualTool)
	w.mustDirectGrant(f.manual)

	task := w.mustTask(w.admin, f.manual, "GRANT")
	if task["canAct"] != false {
		t.Fatalf("the initiator's task = %v", task)
	}
	testutil.RequireStatus(t, act(w.admin, task, "claim", nil), http.StatusForbidden, "separation_of_duties")
	w.mustAct(f.fixer, w.mustAct(f.fixer, task, "claim", nil), "confirm", nil)
	requireGrant(t, w.myGrant(w.dev.c, f.manual), "ACTIVE", "ACTIVE / SELF_REPORTED")
}

func TestDirectGrantRules(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	retired := testutil.CreateLevel(t, w.db, w.jira, "Old", false, false)
	w.exec("UPDATE access_levels SET status = 'RETIRED' WHERE id = $1", retired)
	gone := w.person(testutil.EmployeeSpec{})
	w.exec("UPDATE employees SET status = 'TERMINATED' WHERE id = $1", gone.ID)
	w.approved(w.awsAdmin) // manager only approves the first step; dev has an open AWS item
	held := w.mgr
	w.mustSubmit(held.c, body(nil, item(w.jiraViewer)))
	w.approve(w.skip.c, open(t, items(w.get(held.c, "/api/v1/requests").Body["requests"].([]any)[0].(map[string]any))[0]))
	tl.drain(t)

	cases := []struct {
		name   string
		by     person
		b      map[string]any
		status int
		code   string
	}{
		{"for themselves", w.admin, directBody(w.admin.ID, w.jiraViewer, in(30)), 403, "separation_of_duties"},
		{"not a Super Admin", w.is1, directBody(w.dev.ID, w.jiraViewer, in(30)), 403, ""},
		{"no expiry", w.admin, directBody(w.dev.ID, w.jiraViewer, nil), 400, "expires_required"},
		{"expires today", w.admin, directBody(w.dev.ID, w.jiraViewer, in(0)), 400, "expires_in_past"},
		{"expired", w.admin, directBody(w.dev.ID, w.jiraViewer, in(-1)), 400, "expires_in_past"},
		{"90 days", w.admin, directBody(w.dev.ID, w.jiraViewer, in(90)), 400, "duration_too_long"}, // valid through day 90 (end of day)
		{"not a date", w.admin, directBody(w.dev.ID, w.jiraViewer, "soon"), 400, ""},
		{"blank justification", w.admin, map[string]any{"submissionId": uuid.New(), "beneficiaryId": w.dev.ID,
			"accessLevelId": w.jiraViewer, "justification": " ", "expiresOn": in(30)}, 400, "justification_required"},
		{"retired level", w.admin, directBody(w.dev.ID, retired, in(30)), 400, "level_not_requestable"},
		{"open request for the tool", w.admin, directBody(w.dev.ID, w.awsAdmin, in(30)), 409, "open_request_exists"},
		{"access held", w.admin, directBody(held.ID, w.jiraAdmin, in(30)), 409, "access_exists"},
		{"inactive beneficiary", w.admin, directBody(gone.ID, w.jiraViewer, in(30)), 400, "beneficiary_not_active"},
		{"submission ID of a request", w.admin, map[string]any{"submissionId": w.mustSubmit(w.admin.c, body(&w.skip.ID, item(w.github)))["submissionId"],
			"beneficiaryId": w.dev.ID, "accessLevelId": w.jiraViewer, "justification": "x", "expiresOn": in(30)}, 409, "submission_id_reused"},
		{"89 days", w.admin, directBody(w.dev.ID, w.jiraViewer, in(89)), 201, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { testutil.RequireStatus(t, directGrant(tc.by, tc.b), tc.status, tc.code) })
	}
}

func TestDirectGrantNotifications(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	notes := func(p person) int {
		return w.count("SELECT count(*) FROM notifications WHERE recipient_id = $1 AND kind = 'DIRECT_GRANT'", p.ID)
	}
	w.mustDirectGrant(w.jiraViewer) // Manager only: no approver but the manager is bypassed
	for p, want := range map[*person]int{&w.is1: 1, &w.is2: 1, &w.mgr: 1, &w.owner: 0, &w.dev: 0} {
		if n := notes(*p); n != want {
			t.Fatalf("notifications to %s = %d; want %d", p.Email, n, want)
		}
	}
	w.mustDirectGrant(w.awsAdmin) // requires the Tool Owner: the owner is the bypassed approver
	if notes(w.owner) != 1 || notes(w.mgr) != 2 {
		t.Fatalf("owner %d, manager %d", notes(w.owner), notes(w.mgr))
	}

	// Someone who is bypassed in two ways, here InfoSec and the manager, is told once (BUG-A-02).
	report := w.person(testutil.EmployeeSpec{ManagerID: w.is1.ID})
	testutil.RequireStatus(t, directGrant(w.admin, directBody(report.ID, w.jiraViewer, in(30))), http.StatusCreated, "")
	if notes(w.is1) != 3 || notes(w.is2) != 3 {
		t.Fatalf("InfoSec manager %d, other InfoSec %d; want 3 each", notes(w.is1), notes(w.is2))
	}
}

func TestDirectRevoke(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	f := w.fulfillers()
	w.member(w.admin, f.manualTool)
	w.approved(w.jiraViewer)
	tl.drain(t)

	g := w.myGrant(w.dev.c, w.jiraViewer)
	testutil.RequireStatus(t, revoke(w.admin.c, g, " "), http.StatusBadRequest, "reason_required")
	testutil.RequireStatus(t, revoke(w.is1.c, g, "x"), http.StatusNotFound, "") // only a Super Admin revokes directly
	requireGrant(t, w.mustRevoke(w.admin.c, g), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
	tl.drain(t)
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "REVOKED", "REVOKED / VERIFIED")
	if n := w.count(`SELECT count(*) FROM operations o JOIN audit_events a ON a.action = 'GRANT_REVOCATION_REQUESTED' AND a.entity_id = o.grant_id
		WHERE o.type = 'REVOKE' AND o.reason = 'DIRECT' AND a.actor_id = $1 AND a.details->>'by' = 'DIRECT'`, w.admin.ID); n != 1 {
		t.Fatalf("direct removal = %d", n)
	}

	w.mustRevoke(w.admin.c, w.activeManual(f))
	task := w.mustTask(w.admin, f.manual, "REVOKE")
	if task["canAct"] != false {
		t.Fatalf("the initiator's removal task = %v", task)
	}
	testutil.RequireStatus(t, act(w.admin, task, "claim", nil), http.StatusForbidden, "separation_of_duties")
	w.mustAct(f.fixer, w.mustAct(f.fixer, task, "claim", nil), "confirm", nil)
	requireGrant(t, w.myGrant(w.dev.c, f.manual), "REVOKED", "REVOKED / SELF_REPORTED")
}
