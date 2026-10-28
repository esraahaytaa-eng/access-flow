package requests

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestSubmitJiraViewerNeedsOnlyManager(t *testing.T) {
	w := newWorld(t)
	req := w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer)))
	it := items(req)[0]
	if it["status"] != "PENDING_APPROVAL" || it["tool"] != "Jira" || it["level"] != "Viewer" || it["justification"] != "for the project" {
		t.Fatalf("item = %v", it)
	}
	s := steps(it)
	if len(s) != 1 || s[0]["type"] != "MANAGER" || s[0]["status"] != "PENDING" || s[0]["assigneeId"] != w.mgr.ID.String() ||
		s[0]["routingNote"] != "manager" || s[0]["openedAt"] == nil {
		t.Fatalf("steps = %v; want one pending Manager step for the manager", s)
	}
	if w.audits("REQUEST_SUBMITTED") != 1 {
		t.Fatal("REQUEST_SUBMITTED not audited once")
	}
	if n := w.count("SELECT count(*) FROM email_outbox WHERE recipient_id = $1", w.mgr.ID); n != 1 {
		t.Fatalf("emails to the manager = %d; want 1", n)
	}
}

func TestSubmitAWSAdminHasThreeSteps(t *testing.T) {
	w := newWorld(t)
	s := steps(items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0])
	want := [][2]string{{"MANAGER", "PENDING"}, {"TOOL_OWNER", "WAITING"}, {"INFOSEC", "WAITING"}}
	if len(s) != 3 {
		t.Fatalf("steps = %v", s)
	}
	for i, st := range s {
		if st["type"] != want[i][0] || st["status"] != want[i][1] {
			t.Fatalf("step %d = %v; want %v", i, st, want[i])
		}
	}
	if s[1]["assigneeId"] != nil || s[1]["routingNote"] != nil {
		t.Fatalf("a waiting step is routed when it opens, not before: %v", s[1])
	}
}

func TestOnBehalfByManagerIsApprovedBySubmission(t *testing.T) {
	w := newWorld(t)
	req := w.mustSubmit(w.mgr.c, body(&w.dev.ID, item(w.jiraViewer), item(w.awsAdmin)))
	if req["beneficiary"].(map[string]any)["id"] != w.dev.ID.String() || req["submittedBy"].(map[string]any)["id"] != w.mgr.ID.String() {
		t.Fatalf("request = %v", req)
	}
	viewer, admin := items(req)[0], items(req)[1]
	if viewer["status"] != "APPROVED" || steps(viewer)[0]["status"] != "APPROVED_BY_SUBMISSION" || steps(viewer)[0]["decidedBy"] != w.mgr.ID.String() {
		t.Fatalf("Jira Viewer = %v; want APPROVED by the manager's submission (D3)", viewer)
	}
	if s := steps(admin); s[0]["status"] != "APPROVED_BY_SUBMISSION" || s[1]["status"] != "PENDING" || s[1]["assigneeId"] != w.owner.ID.String() {
		t.Fatalf("AWS Admin steps = %v; want the Tool Owner step open", s)
	}
	if n := w.count("SELECT count(*) FROM email_outbox"); n != 0 {
		t.Fatalf("emails = %d; want none (O2)", n)
	}
	if w.audits("ITEM_APPROVED") != 1 {
		t.Fatal("the approved item was not audited")
	}
	var order string // uuidv7 ids follow insertion order
	_ = w.db.App.QueryRow(context.Background(), `SELECT string_agg(action, ',' ORDER BY id) FROM audit_events
		WHERE action IN ('REQUEST_SUBMITTED', 'ITEM_APPROVED')`).Scan(&order)
	if order != "REQUEST_SUBMITTED,ITEM_APPROVED" {
		t.Fatalf("audit order = %s; want the submission first", order)
	}
}

func TestOnBehalfByOtherNeedsManager(t *testing.T) {
	w := newWorld(t)
	hr := w.person(testutil.EmployeeSpec{Roles: []string{"HR_MANAGER"}})
	for _, submitter := range []person{w.skip, hr} {
		w.exec("UPDATE request_items SET status = 'WITHDRAWN'")
		s := open(t, items(w.mustSubmit(submitter.c, body(&w.dev.ID, item(w.jiraViewer))))[0])
		if s["type"] != "MANAGER" || s["assigneeId"] != w.mgr.ID.String() {
			t.Fatalf("step = %v; want the normal Manager step", s)
		}
	}
	if n := w.count("SELECT count(*) FROM email_outbox WHERE recipient_id = $1", w.mgr.ID); n != 2 {
		t.Fatalf("emails to the manager = %d; want one per submission", n)
	}
}

func TestOneEmailPerSubmission(t *testing.T) {
	w := newWorld(t)
	req := w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer), item(w.awsAdmin), item(w.github)))
	var subject, text string
	if err := w.db.App.QueryRow(context.Background(), "SELECT subject, body FROM email_outbox WHERE request_id = $1", req["id"]).Scan(&subject, &text); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"Jira", "AWS", "GitHub"} {
		if !strings.Contains(text, tool) {
			t.Errorf("email body %q does not name %s", text, tool)
		}
	}
	if n := w.count("SELECT count(*) FROM email_outbox"); n != 1 {
		t.Fatalf("emails = %d; want 1", n)
	}
	// The outbox is readable by InfoSec, not by the employee.
	r := w.get(w.is1.c, "/api/v1/email-outbox")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if len(r.Body["emails"].([]any)) != 1 {
		t.Fatalf("outbox = %v", r.Body)
	}
	testutil.RequireStatus(t, w.get(w.dev.c, "/api/v1/email-outbox"), http.StatusForbidden, "forbidden")
}

func TestNoEmailWhenManagerStepGoesToInfoSec(t *testing.T) {
	w := newWorld(t)
	loner := w.person(testutil.EmployeeSpec{})
	s := open(t, items(w.mustSubmit(loner.c, body(nil, item(w.jiraViewer))))[0])
	if s["pool"] != "INFOSEC" || s["assigneeId"] != nil || s["routingNote"] != "manager missing, manager's manager missing → InfoSec" {
		t.Fatalf("step = %v; want the InfoSec pool with a visible reason", s)
	}
	if n := w.count("SELECT count(*) FROM email_outbox"); n != 0 {
		t.Fatalf("emails = %d; want none: InfoSec gets no email (O2)", n)
	}
}

func TestManagerFallbackWhenManagerInactive(t *testing.T) {
	w := newWorld(t)
	w.exec("UPDATE employees SET status = 'SUSPENDED' WHERE id = $1", w.mgr.ID)
	s := open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer))))[0])
	if s["assigneeId"] != w.skip.ID.String() || s["routingNote"] != "manager inactive → manager's manager" {
		t.Fatalf("step = %v; want the manager's manager", s)
	}
	if n := w.count("SELECT count(*) FROM email_outbox WHERE recipient_id = $1", w.skip.ID); n != 1 {
		t.Fatalf("emails to the manager's manager = %d; want 1", n)
	}
}

func TestRepeatedSubmissionIDReturnsTheOriginal(t *testing.T) {
	w := newWorld(t)
	b := body(nil, item(w.jiraViewer))
	first := w.mustSubmit(w.dev.c, b)
	b["items"] = []map[string]any{item(w.awsAdmin)} // a repeat returns the original, whatever the body
	r := submit(w.dev.c, b)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["id"] != first["id"] || items(r.Body)[0]["level"] != "Viewer" {
		t.Fatalf("repeat = %v; want the original request", r.Body)
	}

	// Concurrent repeats, also for different beneficiaries: one request, never a 500.
	other := w.person(testutil.EmployeeSpec{ManagerID: w.mgr.ID})
	id := uuid.New()
	var wg sync.WaitGroup
	results := make([]testutil.Response, 10)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			beneficiary := w.dev.ID
			if i%2 == 1 {
				beneficiary = other.ID
			}
			results[i] = w.mgr.c.Do(http.MethodPost, "/api/v1/requests", map[string]any{"submissionId": id,
				"beneficiaryId": beneficiary, "items": []map[string]any{item(w.github)}})
		}()
	}
	wg.Wait()
	created := 0
	for _, r := range results {
		if r.Status == http.StatusCreated {
			created++
		} else if r.Status != http.StatusOK {
			t.Fatalf("concurrent repeat = %d %v", r.Status, r.Body)
		}
		if r.Body["submissionId"] != id.String() || r.Body["id"] != results[0].Body["id"] {
			t.Fatalf("concurrent repeats returned different requests: %v", r.Body)
		}
	}
	if created != 1 || w.count("SELECT count(*) FROM requests WHERE submission_id = $1", id) != 1 {
		t.Fatalf("created = %d; want exactly one request", created)
	}
	if w.audits("REQUEST_SUBMITTED") != 2 {
		t.Fatalf("REQUEST_SUBMITTED = %d; want 2 (one per request)", w.audits("REQUEST_SUBMITTED"))
	}
}

func TestSubmissionIDOfAnotherSubmitterIsRejected(t *testing.T) {
	w := newWorld(t)
	b := body(nil, item(w.jiraViewer))
	w.mustSubmit(w.dev.c, b)
	r := submit(w.is1.c, b)
	testutil.RequireStatus(t, r, http.StatusConflict, "submission_id_reused")
	if r.Body["items"] != nil || r.Body["beneficiary"] != nil {
		t.Fatalf("response leaks the original request: %v", r.Body)
	}
}

func TestOpenItemPerToolIsUnique(t *testing.T) {
	w := newWorld(t)
	w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer)))
	r := submit(w.dev.c, body(nil, item(w.github), item(w.jiraAdmin)))
	testutil.RequireStatus(t, r, http.StatusConflict, "open_request_exists")
	if r.Body["field"] != "items[1].accessLevelId" {
		t.Fatalf("field = %v", r.Body["field"])
	}
	r = submit(w.is1.c, body(nil, item(w.jiraViewer), item(w.jiraAdmin)))
	testutil.RequireStatus(t, r, http.StatusBadRequest, "duplicate_tool")
	if r.Body["field"] != "items[1].accessLevelId" {
		t.Fatalf("field = %v", r.Body["field"])
	}
	// Another person may still request the tool.
	w.mustSubmit(w.is1.c, body(nil, item(w.jiraViewer)))
}

func TestInvalidSubmissions(t *testing.T) {
	w := newWorld(t)
	terminated := testutil.CreateEmployee(t, w.db, testutil.EmployeeSpec{Status: "TERMINATED"})
	retiredTool := testutil.CreateTool(t, w.db, testutil.ToolSpec{})
	onRetiredTool := testutil.CreateLevel(t, w.db, retiredTool, "User", false, false)
	w.exec("UPDATE tools SET status = 'RETIRED' WHERE id = $1", retiredTool)
	retiredLevel := testutil.CreateLevel(t, w.db, w.jira, "Old", false, false)
	w.exec("UPDATE access_levels SET status = 'RETIRED' WHERE id = $1", retiredLevel)
	noDate := map[string]any{"type": "TEMPORARY"}
	withDuration := func(d map[string]any) map[string]any { it := item(w.github); it["duration"] = d; return it }
	withJustification := func(j string) map[string]any { it := item(w.github); it["justification"] = j; return it }
	many := make([]map[string]any, 21)
	for i := range many {
		many[i] = item(w.github)
	}
	unknown := uuid.New()
	cases := []struct {
		name        string
		b           map[string]any
		status      int
		code, field string
	}{
		{"no items", body(nil), 400, "items_required", "items"},
		{"too many items", body(nil, many...), 400, "too_many_items", "items"},
		{"one invalid item rejects the whole submission", body(nil, item(w.jiraViewer), withDuration(noDate)), 400, "invalid_duration", "items[1].duration"},
		{"expiry in the past", body(nil, withDuration(map[string]any{"type": "TEMPORARY", "expiresOn": "2000-01-01"})), 400, "expiry_not_in_future", "items[0].duration"},
		{"justification required", body(nil, withJustification("  ")), 400, "justification_required", "items[0].justification"},
		{"justification too long", body(nil, withJustification(strings.Repeat("x", 1001))), 400, "value_too_long", "items[0].justification"},
		{"retired level", body(nil, item(retiredLevel)), 400, "level_not_requestable", "items[0].accessLevelId"},
		{"level of a retired tool", body(nil, item(onRetiredTool)), 400, "level_not_requestable", "items[0].accessLevelId"},
		{"unknown level", body(nil, item(unknown)), 400, "level_not_found", "items[0].accessLevelId"},
		{"terminated beneficiary", body(&terminated.ID, item(w.github)), 400, "beneficiary_not_active", "beneficiaryId"},
		{"unknown beneficiary", body(&unknown, item(w.github)), 400, "beneficiary_not_found", "beneficiaryId"},
		{"missing submission ID", map[string]any{"items": []map[string]any{item(w.github)}}, 400, "field_required", "submissionId"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := submit(w.dev.c, tc.b)
			testutil.RequireStatus(t, r, tc.status, tc.code)
			if r.Body["field"] != tc.field {
				t.Fatalf("field = %v; want %s", r.Body["field"], tc.field)
			}
		})
	}
	if n := w.count("SELECT count(*) FROM requests"); n != 0 {
		t.Fatalf("requests = %d; want none", n)
	}
	if n := w.count("SELECT count(*) FROM audit_events WHERE action LIKE 'REQUEST%'"); n != 0 {
		t.Fatalf("rejected submissions were audited: %d", n)
	}
}

func TestTemporaryDurationIsStored(t *testing.T) {
	w := newWorld(t)
	it := item(w.github)
	it["duration"] = map[string]any{"type": "TEMPORARY", "expiresOn": "2099-06-30"}
	d := items(w.mustSubmit(w.dev.c, body(nil, it)))[0]["duration"].(map[string]any)
	if d["type"] != "TEMPORARY" || d["expiresOn"] != "2099-06-30" {
		t.Fatalf("duration = %v", d)
	}
}

func TestCannotReadOthersRequests(t *testing.T) {
	w := newWorld(t)
	req := w.mustSubmit(w.skip.c, body(&w.dev.ID, item(w.jiraViewer)))
	path := "/api/v1/requests/" + req["id"].(string)
	for _, p := range []person{w.dev, w.skip} { // beneficiary and submitter
		testutil.RequireStatus(t, w.get(p.c, path), http.StatusOK, "")
		if l := w.get(p.c, "/api/v1/requests").Body["requests"].([]any); len(l) != 1 {
			t.Fatalf("list = %v; want the request", l)
		}
	}
	for _, p := range []person{w.is1, w.admin, w.owner} {
		testutil.RequireStatus(t, w.get(p.c, path), http.StatusNotFound, "not_found")
		if l := w.get(p.c, "/api/v1/requests").Body["requests"].([]any); len(l) != 0 {
			t.Fatalf("list = %v; want nothing", l)
		}
	}
	testutil.RequireStatus(t, w.get(w.dev.c, "/api/v1/requests/not-a-uuid"), http.StatusNotFound, "not_found")
}

// A retirement committed while the submission waited for the tool's lock is seen.
func TestSubmitSeesARetirementCommittedWhileWaiting(t *testing.T) {
	w := newWorld(t)
	var got error
	err := testutil.WhileLocked(t, w.db.App, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE tools SET status = 'RETIRED', version = version + 1 WHERE id = $1", w.jira)
		return err
	}, func() error {
		_, _, got = w.svc.Submit(context.Background(), w.dev.principal(), Submission{SubmissionID: uuid.New(),
			Items: []ItemInput{{AccessLevelID: w.jiraViewer, Justification: "x", Duration: Duration{Type: "UNTIL_REVOKED"}}}})
		return got
	})
	var e *platform.Error
	if !errors.As(err, &e) || e.Code != "level_not_requestable" {
		t.Fatalf("submit = %v; want level_not_requestable", err)
	}
}

// Concurrent submissions for one tool, each with its own ID: one item, the rest 409 (A9).
func TestConcurrentSubmissionsForOneTool(t *testing.T) {
	w := newWorld(t)
	var wg sync.WaitGroup
	results := make([]testutil.Response, 10)
	for i := range results {
		wg.Add(1)
		go func() { defer wg.Done(); results[i] = submit(w.dev.c, body(nil, item(w.jiraViewer))) }()
	}
	wg.Wait()
	created := 0
	for _, r := range results {
		switch {
		case r.Status == http.StatusCreated:
			created++
		case r.Status != http.StatusConflict || r.Body["error"] != "open_request_exists":
			t.Fatalf("submit = %d %v", r.Status, r.Body)
		}
	}
	if created != 1 || w.count("SELECT count(*) FROM request_items") != 1 {
		t.Fatalf("created = %d; want one", created)
	}
}
