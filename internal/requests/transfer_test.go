package requests

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// apiLevel is a new API tool's level that needs only the Manager step.
func (w *world) apiLevel(tool string) uuid.UUID {
	w.t.Helper()
	id := testutil.CreateTool(w.t, w.db, testutil.ToolSpec{Name: tool, OwnerID: w.owner.ID})
	return testutil.CreateLevel(w.t, w.db, id, "User", false, false)
}

// employeeGrants is what p sees when reviewing id's access for a transfer.
func (w *world) employeeGrants(p person, id uuid.UUID) testutil.Response {
	w.t.Helper()
	return w.get(p.c, "/api/v1/employees/"+id.String()+"/grants")
}

// HR sees the grants not yet removed: open ones, and those still being removed so a
// leaver's unfinished removals stay visible (D5, D7). Removed ones are not listed.
func TestEmployeeGrants(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	slack := w.apiLevel("Slack")
	w.approved(w.jiraViewer)
	w.approved(slack)
	tl.drain(t)
	w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, slack))
	hr := w.hr()

	r := w.employeeGrants(hr, w.dev.ID)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	statuses := map[string]string{}
	for _, g := range r.Body["grants"].([]any) {
		g := g.(map[string]any)
		statuses[g["accessLevelId"].(string)] = g["status"].(string)
		if g["retainedOnTransfer"] != nil {
			t.Fatalf("retained before any transfer: %v", g)
		}
	}
	if len(statuses) != 2 || statuses[w.jiraViewer.String()] != "ACTIVE" || statuses[slack.String()] != "REVOCATION_REQUESTED" {
		t.Fatalf("grants = %v; want the ACTIVE Jira grant and the Slack grant being removed", statuses)
	}
	tl.drain(t) // the Slack grant is now REVOKED
	if gs := w.employeeGrants(hr, w.dev.ID).Body["grants"].([]any); len(gs) != 1 {
		t.Fatalf("grants after the removal = %v", gs)
	}
	testutil.RequireStatus(t, w.employeeGrants(w.mgr, w.dev.ID), http.StatusForbidden, "")
	testutil.RequireStatus(t, w.employeeGrants(w.dev, w.dev.ID), http.StatusForbidden, "")
	testutil.RequireStatus(t, w.employeeGrants(hr, uuid.New()), http.StatusNotFound, "")
}

// transfer moves id under manager (uuid.Nil: none) into department at its current version, keeping keep.
func (w *world) transfer(by person, id, manager uuid.UUID, department string, keep ...map[string]any) testutil.Response {
	w.t.Helper()
	var managerID any = manager
	if manager == uuid.Nil {
		managerID = nil
	}
	return by.c.Do(http.MethodPost, "/api/v1/employees/"+id.String()+"/transfer", map[string]any{"version": w.version(id),
		"managerId": managerID, "department": department, "jobTitle": nil, "reason": "Reorg 7", "keep": append([]map[string]any{}, keep...)})
}

func keep(g map[string]any, justification string) map[string]any {
	return map[string]any{"grantId": g["id"], "justification": justification}
}

func (w *world) grantStatus(g map[string]any) string {
	w.t.Helper()
	var s string
	if err := w.db.App.QueryRow(context.Background(), "SELECT status FROM grants WHERE id = $1", g["id"]).Scan(&s); err != nil {
		w.t.Fatal(err)
	}
	return s
}

func (w *world) itemStatus(it map[string]any) string {
	w.t.Helper()
	var s string
	if err := w.db.App.QueryRow(context.Background(), "SELECT status || ':' || coalesce(closed_reason, '') FROM request_items WHERE id = $1",
		it["id"]).Scan(&s); err != nil {
		w.t.Fatal(err)
	}
	return s
}

func TestTransferRevokesByDefault(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	f := w.fulfillers()
	w.approved(w.jiraViewer)
	tl.drain(t)
	figma := w.fulfilled(f, f.manual)
	jira := w.myGrant(w.dev.c, w.jiraViewer)
	boss := w.person(testutil.EmployeeSpec{ManagerID: w.skip.ID})

	r := w.transfer(w.hr(), w.dev.ID, boss.ID, "Platform")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["managerId"] != boss.ID.String() || r.Body["department"] != "Platform" {
		t.Fatalf("employee = %v", r.Body)
	}
	if n := w.count(`SELECT count(*) FROM grants g JOIN operations o ON o.grant_id = g.id AND o.type = 'REVOKE'
		WHERE g.employee_id = $1 AND g.status = 'REVOCATION_REQUESTED' AND o.reason = 'TRANSFER' AND o.priority = 1`, w.dev.ID); n != 2 {
		t.Fatalf("grants being removed for the transfer = %d", n)
	}
	tl.drain(t)
	w.mustAct(f.fixer, w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "REVOKE"), "claim", nil), "confirm", nil)
	if w.grantStatus(jira) != "REVOKED" || w.grantStatus(figma) != "REVOKED" {
		t.Fatalf("grants = %s, %s", w.grantStatus(jira), w.grantStatus(figma))
	}
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'EMPLOYEE_TRANSFERRED' AND entity_id = $1 AND reason = 'Reorg 7'
		AND jsonb_array_length(details->'revoked') = 2 AND jsonb_array_length(details->'kept') = 0
		AND details->'to'->>'managerId' = $2 AND details->'from'->>'managerId' = $3`, w.dev.ID, boss.ID, w.mgr.ID); n != 1 {
		t.Fatalf("EMPLOYEE_TRANSFERRED events = %d", n)
	}
}

func TestTransferKeepNeedsJustification(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.approved(w.jiraViewer)
	jira := w.myGrant(w.dev.c, w.jiraViewer)
	boss := w.person(testutil.EmployeeSpec{})
	v := w.version(w.dev.ID)
	testutil.RequireStatus(t, w.transfer(w.hr(), w.dev.ID, boss.ID, "Platform", keep(jira, "  ")), http.StatusBadRequest, "justification_required")
	if w.version(w.dev.ID) != v || w.grantStatus(jira) != "PROVISIONING" ||
		w.count("SELECT count(*) FROM employees WHERE id = $1 AND manager_id = $2 AND status = 'ACTIVE'", w.dev.ID, w.mgr.ID) != 1 {
		t.Fatal("a refused transfer changed something")
	}
}

func TestTransferKeeps(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	f := w.fulfillers()
	temp := item(w.jiraViewer)
	temp["duration"] = map[string]any{"type": "TEMPORARY", "expiresOn": in(30)}
	w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, temp)))[0]))
	tl.drain(t)
	figma := w.fulfilled(f, f.manual)
	jira := w.myGrant(w.dev.c, w.jiraViewer)
	boss := w.person(testutil.EmployeeSpec{})
	hr := w.hr()

	testutil.RequireStatus(t, w.transfer(hr, w.dev.ID, boss.ID, "Platform", keep(jira, " shared on-call rota ")), http.StatusOK, "")
	if w.grantStatus(jira) != "ACTIVE" || w.count("SELECT count(*) FROM grants WHERE id = $1 AND to_char(expires_on, 'YYYY-MM-DD') = $2",
		jira["id"], in(30)) != 1 {
		t.Fatal("the kept grant changed")
	}
	if w.grantStatus(figma) != "REVOCATION_REQUESTED" {
		t.Fatalf("figma = %s", w.grantStatus(figma))
	}
	var kept map[string]any // the Figma grant being removed is listed too
	for _, g := range w.employeeGrants(hr, w.dev.ID).Body["grants"].([]any) {
		if g.(map[string]any)["id"] == jira["id"] {
			kept = g.(map[string]any)
		}
	}
	if kept == nil || kept["retainedOnTransfer"].(map[string]any)["justification"] != "shared on-call rota" {
		t.Fatalf("kept grant = %v", kept)
	}
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'EMPLOYEE_TRANSFERRED'
		AND details->'kept'->0->>'justification' = 'shared on-call rota' AND details->'kept'->0->>'grantId' = $1`, jira["id"]); n != 1 {
		t.Fatalf("kept in the event = %d", n)
	}
	if n := w.count(`SELECT count(*) FROM notifications WHERE recipient_id = $1 AND kind = 'GRANTS_RETAINED_ON_TRANSFER'
		AND details->'grants'->0->>'tool' = 'Jira'`, boss.ID); n != 1 {
		t.Fatalf("retention notifications to the new manager = %d", n)
	}
}

// A grant approved after HR read the list was not reviewed: it is removed (D5).
func TestTransferRevokesGrantCreatedAfterReview(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.approved(w.jiraViewer)
	hr := w.hr()
	reviewed := w.employeeGrants(hr, w.dev.ID).Body["grants"].([]any)[0].(map[string]any)
	slackLevel := w.apiLevel("Slack")
	w.approved(slackLevel)
	slack := w.myGrant(w.dev.c, slackLevel)
	boss := w.person(testutil.EmployeeSpec{})

	testutil.RequireStatus(t, w.transfer(hr, w.dev.ID, boss.ID, "Platform", keep(reviewed, "still needed")), http.StatusOK, "")
	if w.grantStatus(reviewed) != "PROVISIONING" || w.grantStatus(slack) != "CANCELLED" { // never sent: cancelled outright
		t.Fatalf("reviewed = %s, new = %s", w.grantStatus(reviewed), w.grantStatus(slack))
	}
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'EMPLOYEE_TRANSFERRED' AND details->'revoked'->>0 = $1`, slack["id"]); n != 1 {
		t.Fatalf("the new grant listed as revoked = %d", n)
	}
}

// pendingWork gives dev one item waiting for mgr's Manager step (GitHub) and one whose
// Manager step mgr approved (AWS, now with the Tool Owner); dev also holds Jira.
func (w *world) pendingWork() (jira, waiting, approved map[string]any) {
	w.t.Helper()
	w.approved(w.jiraViewer)
	waiting = items(w.mustSubmit(w.dev.c, body(nil, item(w.github))))[0]
	approved = w.approve(w.mgr.c, open(w.t, items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0]))
	return w.myGrant(w.dev.c, w.jiraViewer), waiting, approved
}

func (w *world) managerStep(it map[string]any) (assignee uuid.UUID, version int) {
	w.t.Helper()
	if err := w.db.App.QueryRow(context.Background(), "SELECT assignee_id, version FROM approval_steps WHERE item_id = $1 AND type = 'MANAGER'",
		it["id"]).Scan(&assignee, &version); err != nil {
		w.t.Fatal(err)
	}
	return assignee, version
}

// The new manager decides what the previous one has not; what the previous one approved is
// asked again (D5, 2026-10-29).
func TestTransferManagerStepMovesToNewManager(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	jira, waiting, approved := w.pendingWork()
	boss := w.person(testutil.EmployeeSpec{ManagerID: w.skip.ID})

	testutil.RequireStatus(t, w.transfer(w.hr(), w.dev.ID, boss.ID, "Platform", keep(jira, "still needed")), http.StatusOK, "")
	if assignee, _ := w.managerStep(waiting); assignee != boss.ID || w.itemStatus(waiting) != "PENDING_APPROVAL:" {
		t.Fatalf("waiting item: assignee %v, %s", assignee, w.itemStatus(waiting))
	}
	if w.count("SELECT count(*) FROM audit_events WHERE action = 'STEP_REROUTED' AND details->>'to' = $1", boss.ID) != 1 {
		t.Fatal("no STEP_REROUTED to the new manager")
	}
	if w.itemStatus(approved) != "CANCELLED:transferred" {
		t.Fatalf("item the previous manager approved = %s", w.itemStatus(approved))
	}
	if n := len(w.inbox(boss.c)); n != 1 {
		t.Fatalf("new manager's inbox = %d", n)
	}
}

func TestDepartmentOnlyTransferKeepsSteps(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	jira, waiting, approved := w.pendingWork()
	_, before := w.managerStep(waiting)

	testutil.RequireStatus(t, w.transfer(w.hr(), w.dev.ID, w.mgr.ID, "Platform", keep(jira, "still needed")), http.StatusOK, "")
	if assignee, after := w.managerStep(waiting); assignee != w.mgr.ID || after != before {
		t.Fatalf("Manager step moved: %v, version %d → %d", assignee, before, after)
	}
	if w.audits("STEP_REROUTED") != 0 || w.itemStatus(waiting) != "PENDING_APPROVAL:" || w.itemStatus(approved) != "PENDING_APPROVAL:" {
		t.Fatalf("items = %s, %s", w.itemStatus(waiting), w.itemStatus(approved))
	}
}

func TestTransferRevokeAllCancelsPendingRequests(t *testing.T) {
	t.Parallel()
	for _, newManager := range []bool{true, false} {
		t.Run(map[bool]string{true: "new manager", false: "department only"}[newManager], func(t *testing.T) {
			w := newWorld(t)
			_, waiting, approved := w.pendingWork()
			manager := w.mgr.ID
			if newManager {
				manager = w.person(testutil.EmployeeSpec{}).ID
			}
			testutil.RequireStatus(t, w.transfer(w.hr(), w.dev.ID, manager, "Platform"), http.StatusOK, "")
			if w.itemStatus(waiting) != "CANCELLED:transferred" || w.itemStatus(approved) != "CANCELLED:transferred" || w.audits("STEP_REROUTED") != 0 {
				t.Fatalf("items = %s, %s; reroutes %d", w.itemStatus(waiting), w.itemStatus(approved), w.audits("STEP_REROUTED"))
			}
		})
	}
}

// waiting reports how many statements in this test's database wait for a lock.
func (w *world) waiting() int {
	return w.count("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'")
}

// The previous manager's approval racing the transfer either lands before it or is refused;
// it is never recorded after the transfer (D5).
func TestTransferWhileOldManagerApproves(t *testing.T) {
	t.Parallel()
	for _, approvalFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "approval first", false: "transfer first"}[approvalFirst], func(t *testing.T) {
			w := newWorld(t)
			jira, waiting, _ := w.pendingWork()
			boss := w.person(testutil.EmployeeSpec{})
			hr := w.hr()
			step := open(t, waiting)
			ctx := context.Background()
			tx, err := w.db.App.Begin(ctx) // holds the item: both wait for it
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "SELECT 1 FROM request_items WHERE id = $1 FOR UPDATE", waiting["id"]); err != nil {
				t.Fatal(err)
			}
			decided, transferred := make(chan testutil.Response, 1), make(chan testutil.Response, 1)
			approve := func() { decided <- decide(w.mgr.c, step, "approve", nil) }
			move := func() { transferred <- w.transfer(hr, w.dev.ID, boss.ID, "Platform", keep(jira, "still needed")) }
			first, second := approve, move
			if !approvalFirst {
				first, second = move, approve
			}
			go first()
			w.waitForLock()
			go second()
			for deadline := time.Now().Add(testutil.WaitLimit); w.waiting() < 2; time.Sleep(10 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the second action never waited")
				}
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			r := <-decided
			testutil.RequireStatus(t, <-transferred, http.StatusOK, "")
			late := w.count(`SELECT count(*) FROM audit_events a WHERE a.action = 'STEP_APPROVED' AND a.actor_id = $1 AND a.id >
				(SELECT id FROM audit_events WHERE action = 'EMPLOYEE_TRANSFERRED' AND entity_id = $2)`, w.mgr.ID, w.dev.ID)
			if late != 0 {
				t.Fatalf("the previous manager's approval was recorded after the transfer (approve %d)", r.Status)
			}
			if approvalFirst {
				testutil.RequireStatus(t, r, http.StatusOK, "")
				if w.itemStatus(waiting) != "CANCELLED:transferred" {
					t.Fatalf("approved before the transfer, but the item is %s", w.itemStatus(waiting))
				}
				return
			}
			testutil.RequireStatus(t, r, http.StatusForbidden, "")
			if assignee, _ := w.managerStep(waiting); assignee != boss.ID || w.audits("APPROVAL_ATTEMPT_REJECTED") != 1 ||
				w.itemStatus(waiting) != "PENDING_APPROVAL:" {
				t.Fatalf("after the refused approval: assignee %v, item %s", assignee, w.itemStatus(waiting))
			}
		})
	}
}

// With no grants to review, nothing was revoked: requests follow the normal manager rules
// instead of being cancelled (D5, 2026-10-29).
func TestTransferWithoutGrantsKeepsRequests(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (*world, map[string]any, map[string]any) {
		w := newWorld(t)
		waiting := items(w.mustSubmit(w.dev.c, body(nil, item(w.github))))[0]
		approved := w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0]))
		return w, waiting, approved
	}
	t.Run("department only", func(t *testing.T) {
		w, waiting, approved := setup(t)
		testutil.RequireStatus(t, w.transfer(w.hr(), w.dev.ID, w.mgr.ID, "Platform"), http.StatusOK, "")
		if w.itemStatus(waiting) != "PENDING_APPROVAL:" || w.itemStatus(approved) != "PENDING_APPROVAL:" || w.audits("STEP_REROUTED") != 0 {
			t.Fatalf("items = %s, %s", w.itemStatus(waiting), w.itemStatus(approved))
		}
	})
	t.Run("new manager", func(t *testing.T) {
		w, waiting, approved := setup(t)
		boss := w.person(testutil.EmployeeSpec{})
		testutil.RequireStatus(t, w.transfer(w.hr(), w.dev.ID, boss.ID, "Platform"), http.StatusOK, "")
		if assignee, _ := w.managerStep(waiting); assignee != boss.ID || w.itemStatus(approved) != "CANCELLED:transferred" {
			t.Fatalf("assignee %v, approved item %s", assignee, w.itemStatus(approved))
		}
	})
}

// moveManagerSteps cancels any request the previous reporting line approved, including one the
// manager submitted, keeps one a Super Admin overrode, and routes to the fallback with no manager.
func TestTransferManagerStepCases(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.approved(w.jiraViewer)
	jira := w.myGrant(w.dev.c, w.jiraViewer)
	submitted := items(w.mustSubmit(w.mgr.c, body(&w.dev.ID, item(w.github))))[0] // APPROVED_BY_SUBMISSION
	overridden := items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0]
	testutil.RequireStatus(t, decide(w.admin.c, open(t, overridden), "override", map[string]any{"reason": "incident"}), http.StatusOK, "")

	testutil.RequireStatus(t, w.transfer(w.hr(), w.dev.ID, uuid.Nil, "Platform", keep(jira, "still needed")), http.StatusOK, "")
	if w.itemStatus(submitted) != "CANCELLED:transferred" || w.itemStatus(overridden) != "PENDING_APPROVAL:" {
		t.Fatalf("submitted %s, overridden %s", w.itemStatus(submitted), w.itemStatus(overridden))
	}
	if w.count("SELECT count(*) FROM employees WHERE id = $1 AND manager_id IS NULL", w.dev.ID) != 1 ||
		w.count("SELECT count(*) FROM notifications WHERE kind = 'GRANTS_RETAINED_ON_TRANSFER'") != 0 {
		t.Fatal("transfer to no manager")
	}
}

// A grant suspended while its RESUME is on its way is reviewed like any open grant.
func TestTransferMidResumeGrant(t *testing.T) {
	t.Parallel()
	for _, kept := range []bool{true, false} {
		t.Run(map[bool]string{true: "kept", false: "revoked"}[kept], func(t *testing.T) {
			w := newWorld(t)
			tl := w.startTools()
			hr := w.hr()
			g := w.suspended(tl, hr)
			w.mustLifecycle(hr, w.dev.ID, "reactivate") // the RESUME waits for the runner
			if w.grantStatus(g) != "SUSPENDED" {
				t.Fatalf("grant = %s", w.grantStatus(g))
			}
			var keeps []map[string]any
			if kept {
				keeps = append(keeps, keep(g, "still needed"))
			}
			testutil.RequireStatus(t, w.transfer(hr, w.dev.ID, w.skip.ID, "Platform", keeps...), http.StatusOK, "")
			tl.drain(t)
			if want := map[bool]string{true: "ACTIVE", false: "REVOKED"}[kept]; w.grantStatus(g) != want {
				t.Fatalf("grant = %s; want %s", w.grantStatus(g), want)
			}
		})
	}
}

func TestTransferRules(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.approved(w.jiraViewer)
	jira := w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.jiraViewer)) // being removed
	hr := w.hr()
	boss := w.person(testutil.EmployeeSpec{})
	gone := w.person(testutil.EmployeeSpec{})
	w.mustLifecycle(hr, gone.ID, "suspend")

	testutil.RequireStatus(t, w.transfer(hr, w.dev.ID, w.mgr.ID, "Engineering"), http.StatusBadRequest, "not_a_transfer")
	testutil.RequireStatus(t, w.transfer(hr, hr.ID, boss.ID, "HR"), http.StatusForbidden, "self_change_forbidden")
	testutil.RequireStatus(t, w.transfer(hr, w.is1.ID, boss.ID, "Security"), http.StatusForbidden, "privileged_reset_requires_super_admin")
	testutil.RequireStatus(t, w.transfer(w.owner, w.dev.ID, boss.ID, "Platform"), http.StatusForbidden, "")
	testutil.RequireStatus(t, w.transfer(hr, w.dev.ID, w.dev.ID, "Platform"), http.StatusBadRequest, "manager_cycle")
	testutil.RequireStatus(t, w.transfer(hr, w.mgr.ID, w.dev.ID, "Platform"), http.StatusBadRequest, "manager_cycle")
	testutil.RequireStatus(t, w.transfer(hr, w.dev.ID, gone.ID, "Platform"), http.StatusBadRequest, "manager_inactive")
	testutil.RequireStatus(t, w.transfer(hr, w.dev.ID, uuid.New(), "Platform"), http.StatusBadRequest, "manager_not_found")
	testutil.RequireStatus(t, w.transfer(hr, w.dev.ID, boss.ID, "Platform", map[string]any{"grantId": jira["id"], "justification": "x"}),
		http.StatusBadRequest, "grant_not_open")
	stale := map[string]any{"version": w.version(w.dev.ID) - 1, "managerId": boss.ID, "department": "Platform", "jobTitle": nil,
		"reason": "Reorg 7", "keep": []any{}}
	testutil.RequireStatus(t, hr.c.Do(http.MethodPost, "/api/v1/employees/"+w.dev.ID.String()+"/transfer", stale), http.StatusConflict, "version_conflict")
	noKeep := map[string]any{"version": w.version(w.dev.ID), "managerId": boss.ID, "department": "Platform", "jobTitle": nil, "reason": "Reorg 7"}
	testutil.RequireStatus(t, hr.c.Do(http.MethodPost, "/api/v1/employees/"+w.dev.ID.String()+"/transfer", noKeep), http.StatusBadRequest, "field_required")
	noTitle := map[string]any{"version": w.version(w.dev.ID), "managerId": boss.ID, "department": "Platform", "reason": "Reorg 7", "keep": []any{}}
	testutil.RequireStatus(t, hr.c.Do(http.MethodPost, "/api/v1/employees/"+w.dev.ID.String()+"/transfer", noTitle), http.StatusBadRequest, "field_required")
	testutil.RequireStatus(t, w.transfer(hr, gone.ID, boss.ID, "Platform"), http.StatusConflict, "employee_status")
	if w.audits("EMPLOYEE_TRANSFERRED") != 0 {
		t.Fatal("a refused transfer was recorded")
	}
}

// Two opposite transfers cannot both pass the cycle check (they serialize on the lifecycle lock).
func TestConcurrentTransfersCannotCreateACycle(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	hr1, hr2 := w.hr(), w.hr()
	for range 10 {
		a, b := w.person(testutil.EmployeeSpec{}), w.person(testutil.EmployeeSpec{})
		var wg sync.WaitGroup
		statuses := make([]int, 2)
		for i, pair := range [][2]person{{a, b}, {b, a}} {
			hr := []person{hr1, hr2}[i]
			wg.Go(func() { statuses[i] = w.transfer(hr, pair[0].ID, pair[1].ID, "Platform").Status })
		}
		wg.Wait()
		if statuses[0]+statuses[1] != 200+400 {
			t.Fatalf("statuses = %v", statuses)
		}
	}
}

// An explicit null keep keeps nothing, as [] does.
func TestTransferNullKeepKeepsNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.approved(w.jiraViewer)
	jira := w.myGrant(w.dev.c, w.jiraViewer)
	boss := w.person(testutil.EmployeeSpec{})
	b := map[string]any{"version": w.version(w.dev.ID), "managerId": boss.ID, "department": "Platform", "jobTitle": nil, "reason": "Reorg 7", "keep": nil}
	testutil.RequireStatus(t, w.hr().c.Do(http.MethodPost, "/api/v1/employees/"+w.dev.ID.String()+"/transfer", b), http.StatusOK, "")
	if w.grantStatus(jira) != "CANCELLED" { // never sent: cancelled outright
		t.Fatalf("grant = %s", w.grantStatus(jira))
	}
}

func (w *world) staleGrants() []any {
	w.t.Helper()
	r := w.exceptions(w.is1)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	return r.Body["staleGrants"].([]any)
}

// A grant that activates after its transfer removal was requested is an incident, listed
// until the removal is verified or confirmed (D5).
func TestStaleGrantAfterTransfer(t *testing.T) {
	t.Parallel()
	t.Run("API", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer)
		g := w.myGrant(w.dev.c, w.jiraViewer)
		boss := w.person(testutil.EmployeeSpec{})
		done := w.inFlight(tl)
		testutil.RequireStatus(t, w.transfer(w.hr(), w.dev.ID, boss.ID, "Platform"), http.StatusOK, "")
		if err := done(); err != nil {
			t.Fatal(err)
		}
		if w.count("SELECT count(*) FROM grants WHERE id = $1 AND effective_from IS NOT NULL AND stale_at IS NOT NULL", g["id"]) != 1 ||
			w.audits("STALE_GRANT_INCIDENT") != 1 {
			t.Fatal("no stale grant incident")
		}
		if n := len(w.staleGrants()); n != 1 {
			t.Fatalf("stale grants = %d", n)
		}
		tl.drain(t)
		if w.grantStatus(g) != "REVOKED" || tl.state(t, g, w.dev.Email) != "" || len(w.staleGrants()) != 0 {
			t.Fatalf("after the removal: %s, tool %q, listed %d", w.grantStatus(g), tl.state(t, g, w.dev.Email), len(w.staleGrants()))
		}
	})
	t.Run("Manual", func(t *testing.T) {
		w := newWorld(t)
		f := w.fulfillers()
		w.approved(f.manual)
		g := w.myGrant(w.dev.c, f.manual)
		task := w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "GRANT"), "claim", nil)
		boss := w.person(testutil.EmployeeSpec{})
		testutil.RequireStatus(t, w.transfer(w.hr(), w.dev.ID, boss.ID, "Platform"), http.StatusOK, "")
		w.mustAct(f.fixer, task, "confirm", nil)
		if w.audits("STALE_GRANT_INCIDENT") != 1 || len(w.staleGrants()) != 1 {
			t.Fatal("no stale grant incident")
		}
		w.mustAct(f.fixer, w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "REVOKE"), "claim", nil), "confirm", nil)
		if w.grantStatus(g) != "REVOKED" || len(w.staleGrants()) != 0 {
			t.Fatalf("after the removal: %s, listed %d", w.grantStatus(g), len(w.staleGrants()))
		}
	})
}

func TestNoIncidentWithoutTransfer(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	done := w.inFlight(tl)
	w.mustRevoke(w.dev.c, g)
	if err := done(); err != nil {
		t.Fatal(err)
	}
	if w.count("SELECT count(*) FROM grants WHERE stale_at IS NOT NULL") != 0 || w.audits("STALE_GRANT_INCIDENT") != 0 {
		t.Fatal("an ordinary removal raised a stale grant incident")
	}
}
