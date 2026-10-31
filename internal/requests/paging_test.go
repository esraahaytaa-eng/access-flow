package requests

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// bulkReports gives n new active employees named "<prefix> 0001"…, reporting to manager, an
// imported ACTIVE grant of level each: a large team without n request flows (PERF-004).
func (w *world) bulkReports(n int, manager uuid.UUID, prefix string, level uuid.UUID) {
	w.t.Helper()
	w.exec(`WITH imp AS (INSERT INTO imports (source, imported_by, imported_at, report) VALUES ('MOCK', $1, now(), '{}') RETURNING id),
		emp AS (INSERT INTO employees (email, full_name, manager_id)
			SELECT lower($2) || s || '-' || gen_random_uuid() || '@example.com', $2 || ' ' || lpad(s::text, 4, '0'), $1 FROM generate_series(1, $3::int) s
			RETURNING id)
		INSERT INTO grants (employee_id, tool_id, access_level_id, method, status, import_id, effective_from)
		SELECT emp.id, l.tool_id, l.id, 'API', 'ACTIVE', imp.id, now() FROM emp, imp, access_levels l WHERE l.id = $4`,
		manager, prefix, n, level)
}

// page reads one page of a paged list and returns its rows under key, its total and its body.
func (w *world) page(p person, path, key string) ([]map[string]any, int, map[string]any) {
	w.t.Helper()
	r := w.get(p.c, path)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	var rows []map[string]any
	for _, x := range r.Body[key].([]any) {
		rows = append(rows, x.(map[string]any))
	}
	if r.Body["pageSize"] != float64(platform.PageSize) {
		w.t.Fatalf("%s: pageSize %v", path, r.Body["pageSize"])
	}
	return rows, int(r.Body["total"].(float64)), r.Body
}

// allPages walks every page of path and returns the distinct row ids, failing on a repeat.
func (w *world) allPages(p person, path, key string) map[string]bool {
	w.t.Helper()
	seen := map[string]bool{}
	for n := 1; ; n++ {
		rows, total, _ := w.page(p, fmt.Sprintf("%s&page=%d", path, n), key)
		for _, r := range rows {
			if seen[r["id"].(string)] {
				w.t.Fatalf("%s: row %v on two pages", path, r["id"])
			}
			seen[r["id"].(string)] = true
		}
		if len(rows) < platform.PageSize {
			if len(seen) != total {
				w.t.Fatalf("%s: %d rows reachable, total %d", path, len(seen), total)
			}
			return seen
		}
	}
}

// PERF-004: the review page and the open findings return every authorized item, page by
// page, with exact counts, server-side search and the role's scope.
func TestReviewItemsArePagedCompletely(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.bulkReports(2*platform.PageSize+30, w.mgr.ID, "Rev", w.jiraViewer)
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	want := 2*platform.PageSize + 30

	rows, total, body := w.page(w.owner, "/api/v1/reviews", "items") // the reviewer starts on their own items
	if body["view"] != "mine" || total != want || len(rows) != platform.PageSize || body["page"] != float64(1) {
		t.Fatalf("view %v, total %d, rows %d", body["view"], total, len(rows))
	}
	counts := body["counts"].(map[string]any)
	if counts["mine"] != float64(want) || counts["all"] != float64(want) || counts["done"] != float64(0) {
		t.Fatalf("counts = %v", counts)
	}
	if len(w.allPages(w.owner, "/api/v1/reviews?view=mine", "items")) != want {
		t.Fatal("not every item reachable")
	}
	if rows, total, _ := w.page(w.owner, "/api/v1/reviews?view=mine&q=rev%200042", "items"); total != 1 || len(rows) != 1 {
		t.Fatalf("search over the whole campaign: total %d", total)
	}
	if _, total, _ := w.page(w.is1, "/api/v1/reviews?view=all", "items"); total != want {
		t.Fatalf("InfoSec sees %d", total)
	}
	if _, total, _ := w.page(w.dev, "/api/v1/reviews?view=all", "items"); total != 0 { // not a reviewer: sees none
		t.Fatalf("an employee sees %d items", total)
	}
	testutil.RequireStatus(t, w.get(w.owner.c, "/api/v1/reviews?page=0"), http.StatusBadRequest, "invalid_page")
	testutil.RequireStatus(t, w.get(w.owner.c, "/api/v1/reviews?view=bogus"), http.StatusBadRequest, "invalid_view")

	// Day 21 left them unanswered: every open finding is reachable and counted in Exceptions.
	w.exec("UPDATE review_items SET finding_at = now()")
	ex := w.get(w.is1.c, "/api/v1/exceptions")
	if ex.Body["reviewFindingsTotal"] != float64(want) || len(ex.Body["reviewFindings"].([]any)) != platform.PageSize {
		t.Fatalf("Exceptions: total %v, listed %d", ex.Body["reviewFindingsTotal"], len(ex.Body["reviewFindings"].([]any)))
	}
	if len(w.allPages(w.is1, "/api/v1/reviews?view=findings", "items")) != want {
		t.Fatal("not every finding reachable")
	}
}

// bulkTasks gives n new employees a Manual grant of level waiting in its group's queue.
func (w *world) bulkTasks(n int, prefix string, level uuid.UUID) {
	w.t.Helper()
	w.exec(`WITH imp AS (INSERT INTO imports (source, imported_by, imported_at, report) VALUES ('MOCK', $1, now(), '{}') RETURNING id),
		emp AS (INSERT INTO employees (email, full_name)
			SELECT lower($2) || s || '-' || gen_random_uuid() || '@example.com', $2 || ' ' || lpad(s::text, 4, '0') FROM generate_series(1, $3::int) s
			RETURNING id),
		g AS (INSERT INTO grants (employee_id, tool_id, access_level_id, method, status, import_id)
			SELECT emp.id, l.tool_id, l.id, 'MANUAL', 'PENDING_FULFILLMENT', imp.id FROM emp, imp, access_levels l WHERE l.id = $4
			RETURNING id)
		INSERT INTO operations (grant_id, type, reason, priority) SELECT id, 'GRANT', 'bulk', 2 FROM g`,
		w.admin.ID, prefix, n, level)
}

// PERF-004: a group far behind sees every task, by tab, with exact counts and search.
func TestFulfillmentTasksArePagedCompletely(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.fulfillers()
	want := 2*platform.PageSize + 30
	w.bulkTasks(want, "Task", f.manual)

	rows, total, body := w.page(f.fixer, "/api/v1/fulfillment-tasks?tab=queue", "tasks")
	counts := body["counts"].(map[string]any)
	if total != want || len(rows) != platform.PageSize || counts["queue"] != float64(want) || counts["mine"] != float64(0) || counts["canAct"] != float64(want) {
		t.Fatalf("total %d, rows %d, counts %v", total, len(rows), counts)
	}
	if len(w.allPages(f.fixer, "/api/v1/fulfillment-tasks?tab=queue", "tasks")) != want {
		t.Fatal("not every task reachable")
	}
	testutil.RequireStatus(t, act(f.fixer, rows[0], "claim", nil), http.StatusOK, "")
	if _, total, body := w.page(f.fixer, "/api/v1/fulfillment-tasks?tab=mine", "tasks"); total != 1 || body["counts"].(map[string]any)["queue"] != float64(want-1) {
		t.Fatalf("after a claim: mine %d, counts %v", total, body["counts"])
	}
	if _, total, _ := w.page(f.fixer2, "/api/v1/fulfillment-tasks?tab=others", "tasks"); total != 1 {
		t.Fatalf("others %d", total)
	}
	if _, total, _ := w.page(f.fixer, "/api/v1/fulfillment-tasks?tab=queue&q=task%200150", "tasks"); total != 1 {
		t.Fatalf("search: %d", total)
	}
	if _, total, _ := w.page(w.dev, "/api/v1/fulfillment-tasks?tab=queue", "tasks"); total != 0 { // not a member
		t.Fatalf("a non-member sees %d tasks", total)
	}
	testutil.RequireStatus(t, w.get(f.fixer.c, "/api/v1/fulfillment-tasks?tab=bogus"), http.StatusBadRequest, "invalid_tab")
}

// PERF-004: a lead with a large team sees every report, with their access counted on the
// server, and each report's access on its own; only for their own reports.
func TestTeamIsPagedCompletely(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	want := 2*platform.PageSize + 30
	w.bulkReports(want, w.mgr.ID, "Team", w.jiraViewer)
	w.exec("UPDATE grants SET expires_on = current_date + 3 WHERE employee_id IN (SELECT id FROM employees WHERE full_name = 'Team 0007')")

	_, total, _ := w.page(w.mgr, "/api/v1/access/team?", "members")
	all := w.allPages(w.mgr, "/api/v1/access/team?", "members")
	if len(all) < want || len(all) != total {
		t.Fatalf("members reachable %d, total %d", len(all), total)
	}
	rows, total, _ := w.page(w.mgr, "/api/v1/access/team?q=team%200007", "members")
	if total != 1 || rows[0]["active"] != float64(1) || rows[0]["expiringSoon"] != float64(1) {
		t.Fatalf("search: %d %v", total, rows)
	}
	id := rows[0]["id"].(string)
	r := w.get(w.mgr.c, "/api/v1/access/team/"+id)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if gs := r.Body["grants"].([]any); len(gs) != 1 {
		t.Fatalf("the report's access: %v", gs)
	}
	testutil.RequireStatus(t, w.get(w.owner.c, "/api/v1/access/team/"+id), http.StatusNotFound, "not_found") // not their report
	if _, total, _ := w.page(w.owner, "/api/v1/access/team?", "members"); total != 0 {
		t.Fatalf("the owner has %d reports", total)
	}
}

// Exceptions lists the first rows of each list (oldest or most urgent first) and the exact
// size of every list, so no open record is hidden behind a cap without being counted.
func TestExceptionsCountEveryCappedList(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.fulfillers()
	w.bulkTasks(230, "Failed", f.manual)
	w.exec("UPDATE operations SET status = 'FAILED', last_error = 'tool down', completed_at = now() WHERE reason = 'bulk'")
	w.exec(`WITH imp AS (INSERT INTO imports (source, imported_by, imported_at, report) VALUES ('MOCK', $1, now(), '{}') RETURNING id)
		INSERT INTO grants (employee_id, account, tool_id, access_level_id, method, status, import_id, effective_from)
		SELECT NULL, 'ghost' || s || '@example.com', l.tool_id, l.id, 'API', 'ACTIVE', imp.id, now()
		FROM generate_series(1, 520) s, imp, access_levels l WHERE l.id = $2`, w.admin.ID, w.jiraViewer)

	r := w.get(w.is1.c, "/api/v1/exceptions")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	totals := r.Body["totals"].(map[string]any)
	for list, want := range map[string][2]int{"failedOperations": {200, 230}, "orphanAccounts": {500, 520}, "unroutableSteps": {0, 0},
		"overlaps": {0, 0}, "suspensionGaps": {0, 0}, "securityRevocations": {0, 0}, "staleGrants": {0, 0}} {
		if got := len(r.Body[list].([]any)); got != want[0] || totals[list] != float64(want[1]) {
			t.Errorf("%s: listed %d, total %v; want %d of %d", list, got, totals[list], want[0], want[1])
		}
	}
}

// A long history never pushes a grant that is still held out of an employee's list: grants
// that have not ended come first, then the newest past ones.
func TestHeldGrantsComeBeforeAnyHistory(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.exec(`WITH imp AS (INSERT INTO imports (source, imported_by, imported_at, report) VALUES ('MOCK', $1, now(), '{}') RETURNING id)
		INSERT INTO grants (employee_id, tool_id, access_level_id, method, status, import_id, effective_from, created_at)
		SELECT $2, l.tool_id, l.id, 'API', 'ACTIVE', imp.id, now() - interval '1 year', now() - interval '1 year' FROM imp, access_levels l WHERE l.id = $3`,
		w.admin.ID, w.dev.ID, w.jiraViewer)
	w.exec(`WITH imp AS (INSERT INTO imports (source, imported_by, imported_at, report) VALUES ('MOCK', $1, now(), '{}') RETURNING id)
		INSERT INTO grants (employee_id, tool_id, access_level_id, method, status, import_id, effective_from, effective_to)
		SELECT $2, l.tool_id, l.id, 'API', 'REVOKED', imp.id, now() - interval '1 day', now() FROM generate_series(1, 210), imp, access_levels l WHERE l.id = $3`,
		w.admin.ID, w.dev.ID, w.jiraViewer)
	for _, path := range []string{"/api/v1/access", "/api/v1/access/team/" + w.dev.ID.String()} {
		p := w.dev
		if path != "/api/v1/access" {
			p = w.mgr
		}
		r := w.get(p.c, path)
		testutil.RequireStatus(t, r, http.StatusOK, "")
		if g := r.Body["grants"].([]any)[0].(map[string]any); g["status"] != "ACTIVE" {
			t.Errorf("%s: first grant is %v; want the held ACTIVE one before the history", path, g["status"])
		}
	}
}
