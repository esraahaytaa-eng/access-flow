package requests

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// accessAt is the point-in-time list p sees for the instant at.
func (w *world) accessAt(p person, at time.Time, filter string) (held, removed []map[string]any) {
	w.t.Helper()
	r := w.get(p.c, "/api/v1/audit/access?at="+url.QueryEscape(at.Format(time.RFC3339Nano))+filter)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	for _, g := range r.Body["grants"].([]any) {
		held = append(held, g.(map[string]any))
	}
	for _, g := range r.Body["removed"].([]any) {
		removed = append(removed, g.(map[string]any))
	}
	return held, removed
}

func byGrant(rows []map[string]any, id any) map[string]any {
	for _, r := range rows {
		if r["grantId"] == id {
			return r
		}
	}
	return nil
}

func (w *world) nameOf(id uuid.UUID) string {
	w.t.Helper()
	var name string
	if err := w.db.App.QueryRow(context.Background(), "SELECT full_name FROM employees WHERE id = $1", id).Scan(&name); err != nil {
		w.t.Fatal(err)
	}
	return name
}

// eventAt is when the latest action on entity was recorded.
func (w *world) eventAt(action string, entity any) time.Time {
	w.t.Helper()
	var at time.Time
	if err := w.db.App.QueryRow(context.Background(), `SELECT max(occurred_at) FROM audit_events WHERE action = $1 AND entity_id = $2`,
		action, entity).Scan(&at); err != nil {
		w.t.Fatal(err)
	}
	return at
}

func requireState(t *testing.T, rows []map[string]any, grant any, state, evidence string) map[string]any {
	t.Helper()
	g := byGrant(rows, grant)
	if g == nil {
		t.Fatalf("grant %v not held in %v", grant, rows)
	}
	if g["state"] != state || g["evidence"] != evidence {
		t.Fatalf("grant %v: state %v / %v, want %s / %s", grant, g["state"], g["evidence"], state, evidence)
	}
	return g
}

func TestPointInTimeAtActivationAndRemoval(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	from := w.timeOf("SELECT effective_from FROM grants WHERE id = $1", g["id"])

	if held, _ := w.accessAt(w.is1, from.Add(-time.Microsecond), ""); byGrant(held, g["id"]) != nil {
		t.Fatal("held before its verified activation")
	}
	held, _ := w.accessAt(w.is1, from, "")
	row := requireState(t, held, g["id"], "ACTIVE", "VERIFIED")
	approvers := row["approvers"].([]any)
	if len(approvers) != 1 || approvers[0].(map[string]any)["type"] != "MANAGER" || approvers[0].(map[string]any)["name"] != w.nameOf(w.mgr.ID) ||
		row["employee"].(map[string]any)["id"] != w.dev.ID.String() || row["tool"] != "Jira" || row["level"] != "Viewer" {
		t.Fatalf("row = %v", row)
	}

	tl.faults.FailNext(1, http.StatusConflict) // the tool refuses: the removal fails at once
	testutil.RequireStatus(t, revoke(w.dev.c, g, "no longer needed"), http.StatusOK, "")
	tl.drain(t)
	requested := w.eventAt("GRANT_REVOCATION_REQUESTED", g["id"])
	op := w.timeOf("SELECT created_at FROM operations WHERE grant_id = $1 AND type = 'REVOKE'", g["id"]) // only to sanity-check ordering
	failed := w.timeOf(`SELECT max(a.occurred_at) FROM audit_events a JOIN operations o ON o.id = a.entity_id
		WHERE a.action = 'OPERATION_FAILED' AND o.grant_id = $1`, g["id"])
	if failed.Before(op) {
		t.Fatal("failure recorded before the operation")
	}
	held, _ = w.accessAt(w.is1, requested.Add(-time.Microsecond), "")
	requireState(t, held, g["id"], "ACTIVE", "VERIFIED")
	held, _ = w.accessAt(w.is1, requested, "")
	requireState(t, held, g["id"], "REVOCATION_REQUESTED", "VERIFIED")
	held, _ = w.accessAt(w.is1, failed, "")
	requireState(t, held, g["id"], "REVOCATION_FAILED", "VERIFIED") // O1: a failed removal is still access

	var opID uuid.UUID
	var version int
	if err := w.db.App.QueryRow(context.Background(), "SELECT id, version FROM operations WHERE grant_id = $1 AND type = 'REVOKE'", g["id"]).
		Scan(&opID, &version); err != nil {
		t.Fatal(err)
	}
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodPost, "/api/v1/operations/"+opID.String()+"/retry", map[string]any{"version": version}),
		http.StatusOK, "")
	tl.drain(t)
	to := w.timeOf("SELECT effective_to FROM grants WHERE id = $1", g["id"])
	held, _ = w.accessAt(w.is1, to.Add(-time.Microsecond), "")
	requireState(t, held, g["id"], "REVOCATION_REQUESTED", "VERIFIED") // retried: requested again until verified
	held, removed := w.accessAt(w.is1, to, "")
	if byGrant(held, g["id"]) != nil {
		t.Fatal("held at its verified removal")
	}
	if r := byGrant(removed, g["id"]); r == nil || r["removalEvidence"] != "VERIFIED" || r["effectiveTo"] == nil {
		t.Fatalf("removed in the prior 30 days = %v", removed)
	}
	// A date (end of that day in the company timezone) works as well as an instant.
	r := w.get(w.is1.c, "/api/v1/audit/access?date="+to.In(w.svc.loc).Format(time.DateOnly))
	testutil.RequireStatus(t, r, http.StatusOK, "")
}

func TestPointInTimeEvidenceLabels(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.fulfillers()
	w.approved(f.manual)
	task := w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "GRANT"), "claim", nil)
	w.mustAct(f.fixer, task, "confirm", nil)
	manual := w.myGrant(w.dev.c, f.manual)

	r := w.admin.c.Do(http.MethodPost, "/api/v1/imports/csv", map[string]any{"fileName": "figma.csv",
		"content": "eposta;arac;seviye;hesap\n" + w.mgr.Email + ";Figma;Editor;\n"})
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	var imported uuid.UUID
	if err := w.db.App.QueryRow(context.Background(), "SELECT id FROM grants WHERE employee_id = $1 AND import_id IS NOT NULL", w.mgr.ID).
		Scan(&imported); err != nil {
		t.Fatal(err)
	}

	held, _ := w.accessAt(w.is1, time.Now(), "&tool="+f.manualTool.String())
	if len(held) != 2 {
		t.Fatalf("held = %v", held)
	}
	row := requireState(t, held, manual["id"], "ACTIVE", "SELF_REPORTED")
	if row["confirmedBy"].(map[string]any)["id"] != f.fixer.ID.String() {
		t.Fatalf("confirmed by = %v", row["confirmedBy"])
	}
	row = requireState(t, held, imported.String(), "ACTIVE", "IMPORTED")
	if row["imported"] != true || len(row["approvers"].([]any)) != 0 || row["effectiveFrom"] == nil {
		t.Fatalf("imported row = %v", row) // held from the import, approver unknown (D10)
	}
	if held, _ := w.accessAt(w.is1, time.Now(), "&employee="+w.mgr.ID.String()); len(held) != 1 {
		t.Fatalf("employee filter = %v", held)
	}

	// Removing imported access ends it like any other: confirmed by the admin group.
	testutil.RequireStatus(t, revoke(w.mgr.c, w.myGrant(w.mgr.c, f.manual), "not needed"), http.StatusOK, "")
	task = w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "REVOKE"), "claim", nil)
	w.mustAct(f.fixer, task, "confirm", nil)
	held, removed := w.accessAt(w.is1, time.Now(), "&employee="+w.mgr.ID.String())
	if len(held) != 0 {
		t.Fatalf("removed imported access still held: %v", held)
	}
	if r := byGrant(removed, imported.String()); r == nil || r["removalEvidence"] != "SELF_REPORTED" ||
		r["removedBy"].(map[string]any)["id"] != f.fixer.ID.String() {
		t.Fatalf("removed = %v", removed)
	}
}

func TestAuditExplorerScope(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer) // dev: subject of request, approval and grant events
	tl.drain(t)
	auditor := w.person(testutil.EmployeeSpec{Roles: []string{"AUDITOR"}})
	own := w.mustSubmit(w.owner.c, body(nil, item(w.jiraViewer)))
	w.approve(w.is1.c, open(t, items(own)[0]))
	tl.drain(t)

	subjects := func(p person) map[string]bool {
		t.Helper()
		r := w.get(p.c, "/api/v1/audit/events")
		testutil.RequireStatus(t, r, http.StatusOK, "")
		out := map[string]bool{}
		for _, e := range r.Body["events"].([]any) {
			s, _ := e.(map[string]any)["subject"].(map[string]any)
			if s == nil {
				out[""] = true
				continue
			}
			out[s["id"].(string)] = true
		}
		return out
	}

	testutil.RequireStatus(t, w.get(w.dev.c, "/api/v1/audit/events"), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, w.get(w.dev.c, "/api/v1/audit/access"), http.StatusForbidden, "forbidden")

	// The skip-level manager sees the indirect report's history, not the owner's or system rows.
	skip := subjects(w.skip)
	if !skip[w.dev.ID.String()] || skip[w.owner.ID.String()] || skip[""] {
		t.Fatalf("skip-level manager sees %v", skip)
	}
	held, _ := w.accessAt(w.skip, time.Now(), "")
	if len(held) != 1 || held[0]["employee"].(map[string]any)["id"] != w.dev.ID.String() {
		t.Fatalf("skip-level point-in-time = %v", held)
	}
	testutil.RequireStatus(t, w.get(w.skip.c, "/api/v1/audit/reports/privileged"), http.StatusForbidden, "forbidden")

	// Company-wide, read-only roles see everyone, including system rows.
	for _, p := range []person{auditor, w.is1, w.admin} {
		if all := subjects(p); !all[w.dev.ID.String()] || !all[w.owner.ID.String()] {
			t.Fatalf("%v sees %v", p.Email, all)
		}
	}
	// An Auditor reads the events, point-in-time access and approval evidence, company-wide;
	// no evidence report or action (O7 notes 2026-10-31).
	holders := map[any]bool{}
	held, _ = w.accessAt(auditor, time.Now(), "")
	for _, g := range held {
		holders[g["employee"].(map[string]any)["id"]] = true
	}
	if !holders[w.dev.ID.String()] || !holders[w.owner.ID.String()] {
		t.Fatalf("auditor point-in-time = %v", held)
	}
	testutil.RequireStatus(t, w.get(auditor.c, "/api/v1/audit/requests"), http.StatusOK, "")
	for _, report := range []string{"privileged", "overrides", "policy", "leavers", "quarter"} {
		testutil.RequireStatus(t, w.get(auditor.c, "/api/v1/audit/reports/"+report), http.StatusForbidden, "forbidden")
	}
	testutil.RequireStatus(t, w.startCampaign(auditor), http.StatusForbidden, "forbidden")

	// A former report drops out of the old manager's scope, history included.
	w.exec("UPDATE employees SET manager_id = $2 WHERE id = $1", w.dev.ID, w.owner.ID)
	testutil.RequireStatus(t, w.get(w.mgr.c, "/api/v1/audit/events"), http.StatusForbidden, "forbidden") // no reports left
	if skip := subjects(w.skip); skip[w.dev.ID.String()] {
		t.Fatal("a former report's history is still visible")
	}
	if own := subjects(w.owner); !own[w.dev.ID.String()] {
		t.Fatal("the new manager does not see the report's full history")
	}
}

func TestAuditEventFilters(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.approved(w.jiraViewer)
	q := func(params string) []any {
		t.Helper()
		r := w.get(w.is1.c, "/api/v1/audit/events?"+params)
		testutil.RequireStatus(t, r, http.StatusOK, "")
		return r.Body["events"].([]any)
	}
	if ev := q("action=REQUEST_SUBMITTED"); len(ev) != 1 || ev[0].(map[string]any)["actor"].(map[string]any)["id"] != w.dev.ID.String() {
		t.Fatalf("by action = %v", ev)
	}
	if ev := q("actor=" + w.mgr.ID.String()); len(ev) == 0 {
		t.Fatal("no events by the manager")
	}
	today := time.Now().In(w.svc.loc).Format(time.DateOnly)
	tomorrow := time.Now().In(w.svc.loc).AddDate(0, 0, 1).Format(time.DateOnly)
	if len(q("from="+today+"&to="+today)) == 0 || len(q("from="+tomorrow)) != 0 {
		t.Fatal("date filters")
	}
	if ev := q("q=" + url.QueryEscape(w.nameOf(w.dev.ID)) + "&action=REQUEST_SUBMITTED"); len(ev) != 1 {
		t.Fatalf("text search = %v", ev)
	}
	testutil.RequireStatus(t, w.get(w.is1.c, "/api/v1/audit/events?from=yesterday"), http.StatusBadRequest, "invalid_date")
}

func TestPrivilegedRolesAtAPastDate(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodPost, "/api/v1/employees/"+w.dev.ID.String()+"/roles", map[string]any{"role": "AUDITOR"}),
		http.StatusNoContent, "")
	held := w.timeOf("SELECT valid_from FROM role_assignments WHERE employee_id = $1 AND role = 'AUDITOR'", w.dev.ID)
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodDelete, "/api/v1/employees/"+w.dev.ID.String()+"/roles/AUDITOR", nil), http.StatusNoContent, "")
	ended := w.timeOf("SELECT valid_to FROM role_assignments WHERE employee_id = $1 AND role = 'AUDITOR'", w.dev.ID)

	auditors := func(at time.Time) []map[string]any {
		t.Helper()
		r := w.get(w.is1.c, "/api/v1/audit/reports/privileged?at="+url.QueryEscape(at.Format(time.RFC3339Nano)))
		testutil.RequireStatus(t, r, http.StatusOK, "")
		var out []map[string]any
		for _, h := range r.Body["holders"].([]any) {
			if h := h.(map[string]any); h["role"] == "AUDITOR" {
				out = append(out, h)
			}
		}
		return out
	}
	if a := auditors(held.Add(-time.Microsecond)); len(a) != 0 {
		t.Fatalf("before = %v", a)
	}
	a := auditors(held)
	if len(a) != 1 || a[0]["employee"].(map[string]any)["id"] != w.dev.ID.String() || a[0]["grantedBy"].(map[string]any)["id"] != w.admin.ID.String() {
		t.Fatalf("during = %v", a)
	}
	if a := auditors(ended); len(a) != 0 {
		t.Fatalf("after = %v", a)
	}
}

func TestEvidenceReports(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	r := w.admin.c.Do(http.MethodPost, "/api/v1/direct-grants", map[string]any{"submissionId": uuid.NewString(), "beneficiaryId": w.owner.ID,
		"accessLevelId": w.github, "justification": "incident response access", "expiresOn": time.Now().In(w.svc.loc).AddDate(0, 0, 10).Format(time.DateOnly)})
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	report := func(name string) []any {
		t.Helper()
		r := w.get(w.is1.c, "/api/v1/audit/reports/"+name)
		testutil.RequireStatus(t, r, http.StatusOK, "")
		return r.Body["rows"].([]any)
	}
	if rows := report("overrides"); len(rows) != 1 || rows[0].(map[string]any)["action"] != "DIRECT_GRANT_CREATED" {
		t.Fatalf("overrides = %v", rows)
	}

	w.exec("UPDATE employees SET status = 'TERMINATED' WHERE id = $1", w.dev.ID) // leavers report reads, it does not act
	if rows := report("leavers"); len(rows) != 1 || rows[0].(map[string]any)["status"] != "ACTIVE" {
		t.Fatalf("leavers = %v", rows)
	}
	if rows := report("policy"); len(rows) != 0 {
		t.Fatalf("policy = %v", rows)
	}
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	r = w.get(w.is1.c, "/api/v1/audit/reports/quarter")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["campaign"] == nil || r.Body["counts"].(map[string]any)["total"] == float64(0) {
		t.Fatalf("quarter = %v", r.Body)
	}
}

// auditorOf adds an Auditor once per world.
func (w *world) auditorOf() person {
	if w.auditor.c == nil {
		w.auditor = w.person(testutil.EmployeeSpec{Roles: []string{"AUDITOR"}})
	}
	return w.auditor
}

func TestApprovalEvidenceInScope(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.approved(w.jiraViewer)
	w.mustSubmit(w.owner.c, body(nil, item(w.jiraViewer)))
	testutil.RequireStatus(t, w.get(w.dev.c, "/api/v1/audit/requests"), http.StatusForbidden, "forbidden")

	r := w.get(w.skip.c, "/api/v1/audit/requests")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	reqs := r.Body["requests"].([]any)
	if len(reqs) != 1 {
		t.Fatalf("skip-level manager sees %d requests", len(reqs))
	}
	step := steps(items(reqs[0].(map[string]any))[0])[0]
	if step["type"] != "MANAGER" || step["status"] != "APPROVED" || step["decidedBy"] != w.mgr.ID.String() {
		t.Fatalf("approval chain = %v", step)
	}
	if r := w.get(w.is1.c, "/api/v1/audit/requests?q=Jira"); len(r.Body["requests"].([]any)) != 2 {
		t.Fatalf("InfoSec sees %v", r.Body)
	}
}

// Open findings carried over from an earlier campaign stay in the latest evidence until
// they are resolved (D8 note 2026-10-30), separately from the new campaign's own items.
func TestQuarterEvidenceListsCarriedOverFindings(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	w.age(21)
	w.sweep()
	w.sweep()
	if n := w.count("SELECT count(*) FROM review_items WHERE finding_at IS NOT NULL"); n != 1 {
		t.Fatalf("findings = %d", n)
	}
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")

	r := w.get(w.is1.c, "/api/v1/audit/reports/quarter")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	counts := r.Body["counts"].(map[string]any)
	var carried []map[string]any
	for _, it := range r.Body["items"].([]any) {
		if it := it.(map[string]any); it["carriedOver"] == true {
			carried = append(carried, it)
		}
	}
	if len(carried) != 1 || carried[0]["tool"] != "Jira" || carried[0]["finding"] != true || counts["carriedFindings"] != float64(1) {
		t.Fatalf("carried = %v, counts = %v", carried, counts)
	}
	if counts["total"] != float64(len(r.Body["items"].([]any))-1) {
		t.Fatalf("total = %v counts the carried finding", counts["total"])
	}
}
