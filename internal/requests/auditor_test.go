package requests

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// eventsOf is one page of audit events p sees for query.
func (w *world) eventsOf(p person, query string) (events []map[string]any, total float64) {
	w.t.Helper()
	r := w.get(p.c, "/api/v1/audit/events?"+query)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	for _, e := range r.Body["events"].([]any) {
		events = append(events, e.(map[string]any))
	}
	return events, r.Body["total"].(float64)
}

// An Auditor reads every audit event, filters them and exports all matching events, not only
// a page (O7 note 2026-10-31).
func TestAuditorPagesFiltersAndExportsEveryEvent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer) // Jira: request, approval, grant and operation events
	tl.drain(t)
	jiraGrant := w.myGrant(w.dev.c, w.jiraViewer)["id"]
	gh := w.mustSubmit(w.dev.c, body(nil, item(w.github)))
	auditor := w.auditorOf()
	w.exec(`INSERT INTO audit_events (actor_id, action, entity_type, subject_employee_id, reason)
		SELECT $1, 'QA_BULK', 'test_entity', $2, CASE WHEN n = 1 THEN '=HYPERLINK("x")' ELSE 'row ' || n END
		FROM generate_series(1, 230) n`, w.mgr.ID, w.dev.ID)

	// Paging: every event exactly once, with the exact total.
	seen := map[any]bool{}
	for page := 1; page <= 3; page++ {
		events, total := w.eventsOf(auditor, fmt.Sprintf("action=QA_BULK&page=%d", page))
		if total != 230 || len(events) != map[int]int{1: 100, 2: 100, 3: 30}[page] {
			t.Fatalf("page %d: %d events of %v", page, len(events), total)
		}
		for _, e := range events {
			seen[e["id"]] = true
		}
	}
	if len(seen) != 230 {
		t.Fatalf("paged through %d distinct events, want 230", len(seen))
	}

	// Export: every matching event in one file; text a user wrote cannot start a formula.
	r := w.get(auditor.c, "/api/v1/audit/events/export?action=QA_BULK")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if ct := r.Header.Get("Content-Type"); ct != "text/csv; charset=utf-8" || !strings.Contains(r.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("export headers: %q, %q", ct, r.Header.Get("Content-Disposition"))
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(r.Raw, []byte("\xef\xbb\xbf")))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 231 {
		t.Fatalf("export has %d rows, want a header and 230 events", len(rows))
	}
	formula := 0
	for _, row := range rows[1:] {
		for _, cell := range row {
			if strings.HasPrefix(cell, "=") {
				t.Fatalf("formula cell exported: %q", cell)
			}
			if cell == `'=HYPERLINK("x")` {
				formula++
			}
		}
	}
	if formula != 1 {
		t.Fatalf("neutralised formula cells = %d, want 1", formula)
	}

	// Tool: the events about Jira's requests, grants and operations, nothing else.
	jira, total := w.eventsOf(auditor, "tool="+w.jira.String())
	entities := map[any]bool{}
	for _, e := range jira {
		entities[e["entityId"]] = true
		if e["action"] == "QA_BULK" {
			t.Fatal("an event about no tool matched the tool filter")
		}
	}
	if total == 0 || !entities[jiraGrant] || entities[gh["id"]] {
		t.Fatalf("Jira events (%v): %v", total, jira)
	}

	// Actor and dates.
	today := time.Now().In(w.svc.loc).Format(time.DateOnly)
	tomorrow := time.Now().In(w.svc.loc).AddDate(0, 0, 1).Format(time.DateOnly)
	for q, want := range map[string]float64{
		"actor=" + w.mgr.ID.String():                    230,
		"actor=" + w.owner.ID.String():                  0,
		"from=" + today + "&to=" + today:                230,
		"from=" + tomorrow:                              0,
		"actor=" + w.mgr.ID.String() + "&from=" + today: 230,
	} {
		if _, total := w.eventsOf(auditor, "action=QA_BULK&"+q); total != want {
			t.Errorf("%s: %v events, want %v", q, total, want)
		}
	}
	testutil.RequireStatus(t, w.get(auditor.c, "/api/v1/audit/events?tool=jira"), http.StatusBadRequest, "invalid_id")

	// The filter options come with the audit log, not from the employee directory.
	r = w.get(auditor.c, "/api/v1/audit/filters")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if !strings.Contains(string(r.Raw), w.mgr.ID.String()) || !strings.Contains(string(r.Raw), `"Jira"`) {
		t.Fatalf("filters = %s", r.Raw)
	}
}

// requireDuty asserts a 409 with code and the field that names the duty or input.
func requireDuty(t *testing.T, r testutil.Response, code, field string) {
	t.Helper()
	testutil.RequireStatus(t, r, http.StatusConflict, code)
	if r.Body["field"] != field {
		t.Fatalf("%s: field = %v, want %s", code, r.Body["field"], field)
	}
}

// AUDITOR is refused while the account holds an operational duty, naming the duty to
// resolve first; a plain employee becomes an Auditor (O7 note 2026-10-31).
func TestAuditorRoleRefusedWhileDutiesAreOpen(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	assign := func(p person) testutil.Response {
		return w.admin.c.Do(http.MethodPost, "/api/v1/employees/"+p.ID.String()+"/roles", map[string]any{"role": "AUDITOR"})
	}

	member := w.person(testutil.EmployeeSpec{})
	w.member(member, w.jira)

	approver := w.person(testutil.EmployeeSpec{})
	ownerStep := open(t, w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0])))
	testutil.RequireStatus(t, decide(w.is1.c, ownerStep, "reassign", map[string]any{"assigneeId": approver.ID, "reason": "cover"}),
		http.StatusOK, "")

	reviewer := w.person(testutil.EmployeeSpec{})
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	w.exec("UPDATE review_items SET reviewer_id = $1 WHERE reviewer_id = $2", reviewer.ID, w.owner.ID)
	if w.count("SELECT count(*) FROM review_items WHERE reviewer_id = $1", reviewer.ID) == 0 {
		t.Fatal("no open review item")
	}

	nominee := w.person(testutil.EmployeeSpec{})
	w.mustNominate(w.admin, nominee.ID)

	for _, tc := range []struct {
		p     person
		field string
	}{
		{w.is1, "roles"},
		{member, "adminGroups"},
		{w.owner, "toolOwnership"},
		{w.mgr, "directReports"},
		{approver, "pendingApprovals"},
		{reviewer, "pendingReviews"},
		{nominee, "superAdminNomination"},
	} {
		t.Run(tc.field, func(t *testing.T) { requireDuty(t, assign(tc.p), "auditor_has_duties", tc.field) })
	}
	if n := w.count("SELECT count(*) FROM role_assignments WHERE role = 'AUDITOR'"); n != 0 {
		t.Fatalf("AUDITOR assigned %d times", n)
	}
	testutil.RequireStatus(t, assign(w.dev), http.StatusNoContent, "")
}

// No operational duty is given to an Auditor: roles, admin groups, tool ownership, reports,
// approval steps or a Super Admin nomination (O7 note 2026-10-31).
func TestAuditorGetsNoOperationalDuty(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	au := w.auditorOf()
	hr := w.hr()
	var group string
	if err := w.db.App.QueryRow(context.Background(), "SELECT admin_group_id::text FROM tools WHERE id = $1", w.jira).Scan(&group); err != nil {
		t.Fatal(err)
	}
	ownerStep := open(t, w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0])))

	for _, tc := range []struct {
		name  string
		r     testutil.Response
		field string
	}{
		{"role", w.admin.c.Do(http.MethodPost, "/api/v1/employees/"+au.ID.String()+"/roles", map[string]any{"role": "INFOSEC"}), "role"},
		{"admin group", w.admin.c.Do(http.MethodPost, "/api/v1/admin-groups/"+group+"/members", map[string]any{"employeeId": au.ID}), "employeeId"},
		{"new tool owner", w.admin.c.Do(http.MethodPost, "/api/v1/tools", map[string]any{"name": "Slack", "fulfillmentMethod": "API",
			"ownerId": au.ID, "adminGroupId": group}), "ownerId"},
		{"tool owner change", w.admin.c.Do(http.MethodPut, "/api/v1/tools/"+w.jira.String(), map[string]any{"name": "Jira",
			"ownerId": au.ID, "adminGroupId": group, "version": 1}), "ownerId"},
		{"new employee's manager", hr.c.Do(http.MethodPost, "/api/v1/employees", map[string]any{"email": "new@x.test", "fullName": "New",
			"department": "Platform", "managerId": au.ID}), "managerId"},
		{"transfer to the Auditor", w.transfer(hr, w.dev.ID, au.ID, "Platform"), "managerId"},
		{"step reassigned", decide(w.is1.c, ownerStep, "reassign", map[string]any{"assigneeId": au.ID, "reason": "cover"}), "assigneeId"},
		{"Super Admin nomination", nominate(w.admin, au.ID), "nomineeId"},
	} {
		t.Run(tc.name, func(t *testing.T) { requireDuty(t, tc.r, "employee_is_auditor", tc.field) })
	}
	if n := w.count("SELECT count(*) FROM role_assignments WHERE employee_id = $1 AND valid_to IS NULL", au.ID); n != 1 {
		t.Fatalf("auditor holds %d roles", n)
	}
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodPost, "/api/v1/employees/"+au.ID.String()+"/roles", map[string]any{"role": "AUDITOR"}),
		http.StatusNoContent, "") // holding it already is a no-op
}
