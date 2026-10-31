package requests

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// changeBody asks for level with duration: the held level is an extension, another level
// of the tool a level change.
func changeBody(level uuid.UUID, duration map[string]any) map[string]any {
	return map[string]any{"submissionId": uuid.New(), "accessLevelId": level, "justification": "still on the project",
		"duration": duration}
}

func until(date string) map[string]any { return map[string]any{"type": "TEMPORARY", "expiresOn": date} }

var untilRevoked = map[string]any{"type": "UNTIL_REVOKED"}

func change(c *testutil.Client, g map[string]any, b map[string]any) testutil.Response {
	return c.Do(http.MethodPost, "/api/v1/grants/"+g["id"].(string)+"/change", b)
}

func (w *world) mustChange(c *testutil.Client, g map[string]any, b map[string]any) map[string]any {
	w.t.Helper()
	r := change(c, g, b)
	testutil.RequireStatus(w.t, r, http.StatusCreated, "")
	return r.Body
}

// temporary gives dev an ACTIVE Jira Viewer grant that is valid through date.
func (w *world) temporary(tl *tools, date string) map[string]any {
	w.t.Helper()
	w.approved(w.jiraViewer)
	tl.drain(w.t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	w.expiresOn(g, date)
	return w.myGrant(w.dev.c, w.jiraViewer)
}

func TestExtensionKeepsAccessActive(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	g := w.temporary(tl, in(10))
	emails, ops := w.count("SELECT count(*) FROM email_outbox"), w.count("SELECT count(*) FROM operations")

	req := w.mustChange(w.dev.c, g, changeBody(w.jiraViewer, until(in(40))))
	it := items(req)[0]
	if req["kind"] != "EXTENSION" || it["status"] != "PENDING_APPROVAL" || len(steps(it)) != 1 || open(t, it)["type"] != "MANAGER" ||
		it["duration"].(map[string]any)["expiresOn"] != in(40) || it["fulfillment"] != nil {
		t.Fatalf("extension = %v", req)
	}
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "ACTIVE", "ACTIVE / VERIFIED")

	it = w.approve(w.mgr.c, open(t, it))
	got := w.myGrant(w.dev.c, w.jiraViewer)
	if it["status"] != "APPROVED" || got["status"] != "ACTIVE" || got["expiresOn"] != in(40) {
		t.Fatalf("after approval: item %v, grant %v", it["status"], got)
	}
	if f, _ := it["fulfillment"].(map[string]any); f == nil || f["grantId"] != g["id"] || f["status"] != "ACTIVE / VERIFIED" {
		t.Fatalf("fulfillment = %v", it["fulfillment"])
	}
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'GRANT_EXTENDED' AND entity_id = $1 AND actor_id = $2
		AND details->>'from' = $3 AND details->>'to' = $4`, g["id"], w.mgr.ID, in(10), in(40)); n != 1 {
		t.Fatalf("GRANT_EXTENDED = %d", n)
	}
	if w.count("SELECT count(*) FROM operations") != ops || w.count("SELECT count(*) FROM email_outbox") != emails ||
		w.count("SELECT count(*) FROM grants") != 1 || tl.faults.Requests() != 2 {
		t.Fatal("an extension made an operation, a tool call, a grant or an email (O2)")
	}
}

func TestExtensionToUntilRevoked(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	g := w.temporary(tl, in(10))
	w.approve(w.mgr.c, open(t, items(w.mustChange(w.dev.c, g, changeBody(w.jiraViewer, untilRevoked)))[0]))
	if got := w.myGrant(w.dev.c, w.jiraViewer); got["status"] != "ACTIVE" || got["expiresOn"] != nil {
		t.Fatalf("grant = %v", got)
	}
}

func TestExtensionRules(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	g := w.temporary(tl, in(10))
	forever := w.mgr // holds Jira Viewer until revoked
	w.mustSubmit(forever.c, body(nil, item(w.jiraViewer)))
	w.approve(w.skip.c, open(t, items(w.get(forever.c, "/api/v1/requests").Body["requests"].([]any)[0].(map[string]any))[0]))
	tl.drain(t)
	it := w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.github))))[0])) // Manager → Tool Owner
	w.approve(w.owner.c, open(t, it))
	provisioning := w.myGrant(w.dev.c, w.github) // not drained: still PROVISIONING

	cases := []struct {
		name   string
		by     person
		g      map[string]any
		b      map[string]any
		status int
		code   string
	}{
		{"until revoked already", forever, w.myGrant(forever.c, w.jiraViewer), changeBody(w.jiraViewer, until(in(40))), 400, "not_an_extension"},
		{"an earlier date", w.dev, g, changeBody(w.jiraViewer, until(in(5))), 400, "not_an_extension"},
		{"the same date", w.dev, g, changeBody(w.jiraViewer, until(in(10))), 400, "not_an_extension"},
		{"a date in the past", w.dev, g, changeBody(w.jiraViewer, until(in(-1))), 400, "expiry_not_in_future"},
		{"someone else's grant", w.is1, g, changeBody(w.jiraViewer, until(in(40))), 404, ""},
		{"no justification", w.dev, g, map[string]any{"submissionId": uuid.New(), "accessLevelId": w.jiraViewer,
			"justification": " ", "duration": until(in(40))}, 400, "justification_required"},
		{"not active yet", w.dev, provisioning, changeBody(w.github, untilRevoked), 409, "grant_not_active"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { testutil.RequireStatus(t, change(tc.by.c, tc.g, tc.b), tc.status, tc.code) })
	}
	testutil.RequireStatus(t, change(w.dev.c, map[string]any{"id": uuid.NewString()}, changeBody(w.jiraViewer, untilRevoked)), 404, "")

	t.Run("one pending change per tool", func(t *testing.T) {
		b := changeBody(w.jiraViewer, until(in(40)))
		first := w.mustChange(w.dev.c, g, b)
		again := change(w.dev.c, g, b) // A9: the same submission returns the original
		testutil.RequireStatus(t, again, http.StatusOK, "")
		if again.Body["id"] != first["id"] {
			t.Fatalf("repeat = %v", again.Body)
		}
		testutil.RequireStatus(t, change(w.dev.c, g, changeBody(w.jiraViewer, until(in(50)))), 409, "open_request_exists")
		testutil.RequireStatus(t, submit(w.dev.c, body(nil, item(w.jiraAdmin))), 409, "open_request_exists")
	})
}

// The body names no beneficiary: an inactive employee's grant is a conflict, not a field error.
func TestChangeForAnInactiveEmployee(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	g := w.temporary(tl, in(10))
	w.exec("UPDATE employees SET status = 'SUSPENDED' WHERE id = $1", w.dev.ID)
	r := change(w.mgr.c, g, changeBody(w.jiraViewer, until(in(40))))
	testutil.RequireStatus(t, r, http.StatusConflict, "beneficiary_not_active")
	if r.Body["field"] != nil {
		t.Fatalf("field = %v", r.Body["field"])
	}
}

func TestExtensionByTheManagerOnBehalf(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	g := w.temporary(tl, in(10))
	req := w.mustChange(w.mgr.c, g, changeBody(w.jiraViewer, until(in(40))))
	it := items(req)[0]
	if s := steps(it)[0]; s["status"] != "APPROVED_BY_SUBMISSION" || it["status"] != "APPROVED" { // D3: Manager only
		t.Fatalf("on behalf = %v", it)
	}
	if got := w.myGrant(w.dev.c, w.jiraViewer); got["expiresOn"] != in(40) {
		t.Fatalf("grant = %v", got)
	}
}

func TestExtensionUsesCurrentSteps(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	g := w.temporary(tl, in(10))
	w.exec("UPDATE access_levels SET requires_infosec = true WHERE id = $1", w.jiraViewer)
	got := steps(items(w.mustChange(w.dev.c, g, changeBody(w.jiraViewer, until(in(40)))))[0])
	if len(got) != 2 || got[0]["type"] != "MANAGER" || got[1]["type"] != "INFOSEC" {
		t.Fatalf("steps = %v", got)
	}
}

// Access is valid through its date: an extension approved on that date still applies;
// one approved later, before the sweep ran, finds the access ended.
func TestExtensionApprovedAroundTheExpiryDate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, expires string
		status        string
	}{
		{"on the expiry date", in(0), "APPROVED"},
		{"the day after, before the sweep", in(-1), "CANCELLED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			tl := w.startTools()
			g := w.temporary(tl, in(10))
			req := w.mustChange(w.dev.c, g, changeBody(w.jiraViewer, until(in(40))))
			w.expiresOn(g, tc.expires)
			it := w.approve(w.mgr.c, open(t, items(req)[0]))
			got := w.myGrant(w.dev.c, w.jiraViewer)
			if it["status"] != tc.status {
				t.Fatalf("item %v (%v)", it["status"], it["closedReason"])
			}
			if tc.status == "CANCELLED" && (it["closedReason"] != "the access has ended" || got["expiresOn"] != tc.expires) {
				t.Fatalf("item %v, grant %v", it, got)
			}
			if tc.status == "APPROVED" && got["expiresOn"] != in(40) {
				t.Fatalf("grant %v", got)
			}
		})
	}
}

func TestClosingAGrantCancelsItsPendingChange(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		close func(w *world, g map[string]any)
	}{
		{"expiry", func(w *world, g map[string]any) { w.expiresOn(g, in(-1)); w.sweep() }},
		{"revoke", func(w *world, g map[string]any) { w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.jiraViewer)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			tl := w.startTools()
			g := w.temporary(tl, in(10))
			it := items(w.mustChange(w.dev.c, g, changeBody(w.jiraViewer, until(in(40)))))[0]
			tc.close(w, g)
			r := w.get(w.dev.c, "/api/v1/requests/"+it["requestId"].(string))
			closed := items(r.Body)[0]
			if closed["status"] != "CANCELLED" || closed["closedReason"] != "the access has ended" || steps(closed)[0]["status"] != "CANCELLED" {
				t.Fatalf("pending extension after close = %v", closed)
			}
			if n := w.count("SELECT count(*) FROM audit_events WHERE action = 'ITEM_CANCELLED' AND entity_id = $1 AND reason = 'the access has ended'",
				it["id"]); n != 1 {
				t.Fatalf("ITEM_CANCELLED = %d", n)
			}
			testutil.RequireStatus(t, decide(w.mgr.c, open(t, it), "approve", nil), 409, "item_not_pending")
			tl.drain(t)
			w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer))) // nothing blocks the next request
		})
	}
}

func TestItemApprovedAfterItsExpiry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, expires, status string
	}{
		{"its date passed", in(-1), "CANCELLED"},
		{"its date is today", in(0), "APPROVED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			temp := item(w.jiraViewer)
			temp["duration"] = until(in(5))
			it := items(w.mustSubmit(w.dev.c, body(nil, temp)))[0]
			w.exec("UPDATE request_items SET expires_on = $2 WHERE id = $1", it["id"], tc.expires)
			it = w.approve(w.mgr.c, open(t, it))
			grants := w.count("SELECT count(*) FROM grants")
			if it["status"] != tc.status || (tc.status == "CANCELLED") != (grants == 0) {
				t.Fatalf("item %v (%v), grants %d", it["status"], it["closedReason"], grants)
			}
			if tc.status == "CANCELLED" && it["closedReason"] != "expired before approval" {
				t.Fatalf("reason = %v", it["closedReason"])
			}
		})
	}
}

// D1 (2026-10-28): a direct grant is extended only through the normal workflow, which
// re-runs the steps it bypassed, and may then become Until Revoked.
func TestDirectGrantExtendedThroughTheWorkflow(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.mustDirectGrant(w.jiraViewer)
	tl.drain(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	it := items(w.mustChange(w.dev.c, g, changeBody(w.jiraViewer, untilRevoked)))[0]
	if it["status"] != "PENDING_APPROVAL" || open(t, it)["type"] != "MANAGER" {
		t.Fatalf("extension of a direct grant = %v", it)
	}
	w.approve(w.mgr.c, open(t, it))
	if got := w.myGrant(w.dev.c, w.jiraViewer); got["expiresOn"] != nil || got["status"] != "ACTIVE" {
		t.Fatalf("grant = %v", got)
	}
}

// An approved extension is already in force: withdrawing it would leave the new date in
// place behind a WITHDRAWN item.
func TestApprovedExtensionCannotBeWithdrawn(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	g := w.temporary(tl, in(10))
	it := w.approve(w.mgr.c, open(t, items(w.mustChange(w.dev.c, g, changeBody(w.jiraViewer, until(in(40)))))[0]))
	testutil.RequireStatus(t, withdraw(w.dev.c, it, "changed my mind"), http.StatusConflict, "fulfillment_started")
	if got := w.myGrant(w.dev.c, w.jiraViewer); got["expiresOn"] != in(40) {
		t.Fatalf("grant = %v", got)
	}
}

// ENG-006: extensions and level changes on behalf follow the same rule as new requests (D3
// note 2026-10-30): the management chain, an HR Manager or a Super Admin; anyone else gets
// 404. Only the direct manager's submission approves the Manager step.
func TestChangeOnBehalfFollowsTheManagementChain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	g := w.temporary(tl, in(10))
	hr := w.hr()
	for _, p := range []person{w.owner, w.is1} { // no line to dev, no HR or Super Admin role
		testutil.RequireStatus(t, change(p.c, g, changeBody(w.jiraViewer, until(in(40)))), http.StatusNotFound, "not_found")
	}
	for _, p := range []person{w.skip, hr, w.admin} {
		it := items(w.mustChange(p.c, g, changeBody(w.jiraViewer, until(in(40)))))[0]
		if s := steps(it)[0]; s["type"] != "MANAGER" || s["status"] != "PENDING" || s["assigneeId"] != w.mgr.ID.String() {
			t.Fatalf("%v: the Manager step = %v", p.ID, s)
		}
		w.exec("UPDATE request_items SET status = 'WITHDRAWN' WHERE id = $1", it["id"])
	}
}
