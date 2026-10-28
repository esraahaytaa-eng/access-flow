package requests

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func withdraw(c *testutil.Client, it map[string]any, reason any) testutil.Response {
	return c.Do(http.MethodPost, "/api/v1/request-items/"+it["id"].(string)+"/withdraw", map[string]any{"version": it["version"], "reason": reason})
}

func TestWithdrawBeforeDecision(t *testing.T) {
	w := newWorld(t)
	req := w.mustSubmit(w.skip.c, body(&w.dev.ID, item(w.awsAdmin), item(w.jiraViewer)))
	aws, jira := items(req)[0], items(req)[1]
	for _, p := range []person{w.mgr, w.is1, w.admin} { // the approver, InfoSec and a Super Admin do not withdraw
		testutil.RequireStatus(t, withdraw(p.c, aws, "x"), http.StatusNotFound, "not_found")
	}
	testutil.RequireStatus(t, withdraw(w.dev.c, aws, " "), http.StatusBadRequest, "reason_required")
	for _, c := range []struct {
		p  person
		it map[string]any
	}{{w.dev, aws}, {w.skip, jira}} { // the beneficiary and the submitter
		r := withdraw(c.p.c, c.it, "no longer needed")
		testutil.RequireStatus(t, r, http.StatusOK, "")
		if r.Body["status"] != "WITHDRAWN" {
			t.Fatalf("item = %v", r.Body)
		}
		for _, s := range steps(r.Body) {
			if s["status"] != "CANCELLED" {
				t.Fatalf("step = %v; want CANCELLED", s)
			}
		}
	}
	testutil.RequireStatus(t, withdraw(w.dev.c, aws, "again"), http.StatusOK, "") // a repeat returns the current state
	if w.audits("ITEM_WITHDRAWN") != 2 {
		t.Fatalf("ITEM_WITHDRAWN = %d; want 2", w.audits("ITEM_WITHDRAWN"))
	}
	if l := w.inbox(w.mgr.c); len(l) != 0 {
		t.Fatalf("manager's inbox = %v; want empty", l)
	}
}

func TestCannotWithdrawADecidedItem(t *testing.T) {
	w := newWorld(t)
	approved := w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer))))[0]))
	testutil.RequireStatus(t, withdraw(w.dev.c, approved, "x"), http.StatusConflict, "item_not_pending")
	r := decide(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.github))))[0]), "reject", map[string]any{"reason": "no"})
	testutil.RequireStatus(t, withdraw(w.dev.c, r.Body, "x"), http.StatusConflict, "item_not_pending")
}

func TestReassignRespectsSeparationOfDuties(t *testing.T) {
	w := newWorld(t)
	terminated := testutil.CreateEmployee(t, w.db, testutil.EmployeeSpec{Status: "TERMINATED"})
	it := w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0]))
	owner := open(t, it) // Tool Owner step, assigned to the owner
	reassign := func(p person, s map[string]any, to any) testutil.Response {
		return decide(p.c, s, "reassign", map[string]any{"assigneeId": to, "reason": "owner on leave"})
	}
	cases := []struct {
		name   string
		to     any
		status int
		code   string
	}{
		{"to the beneficiary", w.dev.ID, 400, "separation_of_duties"},
		{"to an earlier approver", w.mgr.ID, 400, "separation_of_duties"},
		{"to a terminated employee", terminated.ID, 400, "assignee_not_active"},
		{"to the current assignee", w.owner.ID, 400, "already_assigned"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { testutil.RequireStatus(t, reassign(w.is1, owner, tc.to), tc.status, tc.code) })
	}
	testutil.RequireStatus(t, reassign(w.mgr, owner, w.skip.ID), http.StatusForbidden, "forbidden") // not InfoSec or Super Admin

	r := reassign(w.is1, owner, w.skip.ID)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	s := open(t, r.Body)
	if s["assigneeId"] != w.skip.ID.String() || s["routingNote"] != "reassigned: owner on leave" || s["openedAt"] != owner["openedAt"] {
		t.Fatalf("step = %v; want skip, a visible note and the original waiting time", s)
	}
	if w.audits("STEP_REASSIGNED") != 1 || w.count("SELECT count(*) FROM notifications WHERE recipient_id = $1 AND kind = 'APPROVAL_ASSIGNED'", w.skip.ID) != 1 {
		t.Fatal("reassignment not audited or the new assignee not notified")
	}
	testutil.RequireStatus(t, decide(w.owner.c, s, "approve", nil), http.StatusForbidden, "forbidden")
	infosec := open(t, w.approve(w.skip.c, s))

	// An InfoSec step goes only to an InfoSec member.
	testutil.RequireStatus(t, reassign(w.admin, infosec, w.owner.ID), http.StatusBadRequest, "assignee_not_infosec")
	r = reassign(w.admin, infosec, w.is2.ID)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if l := w.inbox(w.is1.c); len(l) != 0 {
		t.Fatalf("is1's inbox = %v; the step now belongs to is2", l)
	}
	w.approve(w.is2.c, open(t, r.Body))
}

func TestNobodyReassignsTheirOwnItem(t *testing.T) {
	w := newWorld(t)
	w.exec("UPDATE employees SET manager_id = $2 WHERE id = $1", w.is1.ID, w.mgr.ID)
	s := open(t, items(w.mustSubmit(w.is1.c, body(nil, item(w.jiraViewer))))[0])
	testutil.RequireStatus(t, decide(w.is1.c, s, "reassign", map[string]any{"assigneeId": w.is2.ID, "reason": "x"}),
		http.StatusForbidden, "separation_of_duties")
}

func TestOverrideRecordsTheSuperAdmin(t *testing.T) {
	w := newWorld(t)
	s := open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0])
	testutil.RequireStatus(t, decide(w.is1.c, s, "override", map[string]any{"reason": "x"}), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, decide(w.admin.c, s, "override", map[string]any{"reason": ""}), http.StatusBadRequest, "reason_required")
	r := decide(w.admin.c, s, "override", map[string]any{"reason": "manager unreachable, incident"})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	st := steps(r.Body)[0]
	if st["status"] != "SUPER_ADMIN_OVERRIDE" || st["decidedBy"] != w.admin.ID.String() || st["assigneeId"] != w.mgr.ID.String() ||
		st["reason"] != "manager unreachable, incident" {
		t.Fatalf("step = %v; want the override by the Super Admin, the original assignee kept (D1)", st)
	}
	if next := open(t, r.Body); next["type"] != "TOOL_OWNER" {
		t.Fatalf("next step = %v", next)
	}
	if w.audits("SUPER_ADMIN_OVERRIDE") != 1 || w.audits("STEP_APPROVED") != 0 {
		t.Fatal("the override is not recorded as an override")
	}
	for _, p := range []person{w.is1, w.is2, w.mgr} { // InfoSec, the manager (also the bypassed assignee)
		if n := w.count("SELECT count(*) FROM notifications WHERE recipient_id = $1 AND kind = 'SUPER_ADMIN_OVERRIDE'", p.ID); n != 1 {
			t.Fatalf("notifications to %s = %d; want 1", p.Email, n)
		}
	}
	// The admin overrode a step, so they may not override or decide another on this item.
	testutil.RequireStatus(t, decide(w.admin.c, open(t, r.Body), "override", map[string]any{"reason": "x"}), http.StatusForbidden, "separation_of_duties")
}

func TestOverrideNotifiesTheBypassedAssignee(t *testing.T) {
	w := newWorld(t)
	s := open(t, w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.github))))[0])))
	testutil.RequireStatus(t, decide(w.admin.c, s, "override", map[string]any{"reason": "owner away"}), http.StatusOK, "")
	if n := w.count("SELECT count(*) FROM notifications WHERE recipient_id = $1 AND kind = 'SUPER_ADMIN_OVERRIDE'", w.owner.ID); n != 1 {
		t.Fatalf("bypassed owner notified %d times; want 1", n)
	}
}

func TestOverrideCannotReverseARejection(t *testing.T) {
	w := newWorld(t)
	s := open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0])
	r := decide(w.mgr.c, s, "reject", map[string]any{"reason": "no"})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	for _, st := range steps(r.Body) {
		testutil.RequireStatus(t, decide(w.admin.c, st, "override", map[string]any{"reason": "x"}), http.StatusConflict, "item_not_pending")
	}
	if w.audits("SUPER_ADMIN_OVERRIDE") != 0 {
		t.Fatal("a rejection was overridden")
	}
}

func TestNoOverrideOnOwnItem(t *testing.T) {
	w := newWorld(t)
	w.exec("UPDATE employees SET manager_id = $2 WHERE id = $1", w.admin.ID, w.mgr.ID)
	s := open(t, items(w.mustSubmit(w.admin.c, body(nil, item(w.jiraViewer))))[0])
	testutil.RequireStatus(t, decide(w.admin.c, s, "override", map[string]any{"reason": "x"}), http.StatusForbidden, "separation_of_duties")
	// Nor on a request they submitted for someone else.
	s = open(t, items(w.mustSubmit(w.admin.c, body(&w.dev.ID, item(w.github))))[0])
	testutil.RequireStatus(t, decide(w.admin.c, s, "override", map[string]any{"reason": "x"}), http.StatusForbidden, "separation_of_duties")
}

func TestUnroutableStepListedAndResolvedByOverride(t *testing.T) {
	w := newWorld(t)
	// The only InfoSec member left is the beneficiary: nobody may decide the pool step.
	w.exec("UPDATE role_assignments SET valid_to = now() WHERE employee_id = $1", w.is2.ID)
	loner := w.person(testutil.EmployeeSpec{Roles: []string{"INFOSEC"}})
	w.exec("UPDATE role_assignments SET valid_to = now() WHERE employee_id = $1", w.is1.ID)
	s := open(t, items(w.mustSubmit(loner.c, body(nil, item(w.jiraViewer))))[0])
	if s["pool"] != "INFOSEC" {
		t.Fatalf("step = %v", s)
	}
	testutil.RequireStatus(t, w.get(w.dev.c, "/api/v1/approvals/open"), http.StatusForbidden, "forbidden")
	r := w.get(w.admin.c, "/api/v1/approvals/open")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	open := r.Body["approvals"].([]any)
	if len(open) != 1 || open[0].(map[string]any)["unroutable"] != true {
		t.Fatalf("open approvals = %v; want the step marked unroutable", open)
	}
	r = decide(w.admin.c, s, "override", map[string]any{"reason": "no eligible InfoSec member"})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["status"] != "APPROVED" {
		t.Fatalf("item = %v", r.Body)
	}

	// With an eligible member again, a pool step is routable.
	w.exec("INSERT INTO role_assignments (employee_id, role) VALUES ($1, 'INFOSEC')", w.is1.ID)
	w.mustSubmit(w.person(testutil.EmployeeSpec{}).c, body(nil, item(w.jiraViewer)))
	if l := w.get(w.is1.c, "/api/v1/approvals/open").Body["approvals"].([]any); len(l) != 1 || l[0].(map[string]any)["unroutable"] != false {
		t.Fatalf("open approvals = %v; want one routable step", l)
	}
}

// A role removal committed while the reassignment waited for the target's row lock is seen.
func TestReassignSeesARoleRemovalCommittedWhileWaiting(t *testing.T) {
	w := newWorld(t)
	loner := w.person(testutil.EmployeeSpec{})
	s := open(t, items(w.mustSubmit(loner.c, body(nil, item(w.jiraViewer))))[0]) // Manager step in the InfoSec pool
	stepID := uuid.MustParse(s["id"].(string))
	is2 := w.is2.ID
	// Make it an InfoSec step for the role check.
	w.exec("UPDATE approval_steps SET type = 'INFOSEC' WHERE id = $1", stepID)
	err := testutil.WhileLocked(t, w.db.App, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", is2); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE role_assignments SET valid_to = now() WHERE employee_id = $1", is2)
		return err
	}, func() error {
		_, err := w.svc.Reassign(context.Background(), w.admin.principal("SUPER_ADMIN"), stepID, 2, is2, "cover")
		return err
	})
	var e *platform.Error
	if !errors.As(err, &e) || e.Code != "assignee_not_infosec" {
		t.Fatalf("reassign = %v; want assignee_not_infosec", err)
	}
}

// F2: an item keeps the steps it was submitted with; later level changes do not alter them.
func TestStepsAreSnapshottedAtSubmission(t *testing.T) {
	w := newWorld(t)
	req := w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin)))
	w.exec("UPDATE access_levels SET requires_infosec = false, requires_tool_owner = false WHERE id = $1", w.awsAdmin)
	r := w.get(w.dev.c, "/api/v1/requests/"+req["id"].(string))
	if s := steps(items(r.Body)[0]); len(s) != 3 || s[2]["type"] != "INFOSEC" {
		t.Fatalf("steps = %v; want the three submitted steps", s)
	}
}

// Reassigning a step to yourself would turn an override into an ordinary approval and skip
// D1's record and notifications; an earlier approver may not pick the next decider either.
func TestReassignPermissionMatrix(t *testing.T) {
	w := newWorld(t)
	auditor := w.person(testutil.EmployeeSpec{Roles: []string{"AUDITOR"}})
	hr := w.person(testutil.EmployeeSpec{Roles: []string{"HR_MANAGER"}})
	it := w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.github))))[0]))
	s := open(t, it) // Tool Owner step
	reassign := func(p person, to any) testutil.Response {
		return decide(p.c, s, "reassign", map[string]any{"assigneeId": to, "reason": "cover"})
	}
	for _, p := range []person{w.dev, w.mgr, w.owner, auditor, hr} {
		testutil.RequireStatus(t, reassign(p, w.skip.ID), http.StatusForbidden, "forbidden")
	}
	testutil.RequireStatus(t, reassign(w.admin, w.admin.ID), http.StatusBadRequest, "self_assignment")
	testutil.RequireStatus(t, reassign(w.is1, w.is1.ID), http.StatusBadRequest, "self_assignment")

	// is1 decides the InfoSec-pool Manager step of another item, then may not reassign its next step.
	w.exec("UPDATE employees SET manager_id = NULL WHERE id = $1", w.dev.ID)
	pool := open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0])
	next := open(t, w.approve(w.is1.c, pool))
	testutil.RequireStatus(t, decide(w.is1.c, next, "reassign", map[string]any{"assigneeId": w.skip.ID, "reason": "x"}),
		http.StatusForbidden, "separation_of_duties")
	if w.audits("STEP_REASSIGNED") != 0 {
		t.Fatal("a refused reassignment was applied")
	}
}
