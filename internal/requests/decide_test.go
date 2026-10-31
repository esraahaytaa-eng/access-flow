package requests

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// The CASE flow: AWS Admin needs Manager → Tool Owner → InfoSec, three different people.
func TestAWSAdminNeedsThreeDifferentPeople(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	it := items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0]
	it = w.approve(w.mgr.c, open(t, it))
	s := open(t, it)
	if s["type"] != "TOOL_OWNER" || s["assigneeId"] != w.owner.ID.String() || s["routingNote"] != "tool owner" {
		t.Fatalf("second step = %v", s)
	}
	it = w.approve(w.owner.c, s)
	s = open(t, it)
	if s["type"] != "INFOSEC" || s["pool"] != "INFOSEC" || s["routingNote"] != "InfoSec" {
		t.Fatalf("third step = %v", s)
	}
	it = w.approve(w.is1.c, s)
	if it["status"] != "APPROVED" || it["closedAt"] == nil {
		t.Fatalf("item = %v; want APPROVED", it)
	}
	deciders := map[any]bool{}
	for _, st := range steps(it) {
		if st["status"] != "APPROVED" {
			t.Fatalf("step = %v", st)
		}
		deciders[st["decidedBy"]] = true
	}
	if len(deciders) != 3 {
		t.Fatalf("deciders = %v; want three different people", deciders)
	}
	if w.audits("STEP_APPROVED") != 3 || w.audits("ITEM_APPROVED") != 1 {
		t.Fatal("decisions not audited")
	}
}

func TestJiraViewerApprovedByManagerAlone(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	it := w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer))))[0]))
	if it["status"] != "APPROVED" {
		t.Fatalf("item = %v", it)
	}
}

// The manager is also the Tool Owner: the Tool Owner step goes to InfoSec, and the two
// InfoSec steps need two different members.
func TestManagerWhoIsToolOwnerGoesToInfoSec(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.exec("UPDATE tools SET owner_id = $1", w.mgr.ID)
	it := w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0]))
	s := open(t, it)
	if s["type"] != "TOOL_OWNER" || s["pool"] != "INFOSEC" || s["routingNote"] != "tool owner is an earlier approver → InfoSec" {
		t.Fatalf("Tool Owner step = %v", s)
	}
	it = w.approve(w.is1.c, s)
	s = open(t, it)
	testutil.RequireStatus(t, decide(w.is1.c, s, "approve", nil), http.StatusForbidden, "separation_of_duties")
	if inbox := w.inbox(w.is1.c); len(inbox) != 0 {
		t.Fatalf("is1's inbox = %v; want empty: they decided the earlier step", inbox)
	}
	if it = w.approve(w.is2.c, s); it["status"] != "APPROVED" {
		t.Fatalf("item = %v", it)
	}
}

func TestBeneficiaryCannotApprove(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	// The beneficiary is InfoSec and the tool owner: they appear in neither queue.
	w.exec("UPDATE tools SET owner_id = $1", w.is1.ID)
	w.exec("UPDATE employees SET manager_id = $2 WHERE id = $1", w.is1.ID, w.mgr.ID)
	it := w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.is1.c, body(nil, item(w.awsAdmin))))[0]))
	s := open(t, it)
	if s["routingNote"] != "tool owner is the beneficiary → InfoSec" {
		t.Fatalf("Tool Owner step = %v", s)
	}
	testutil.RequireStatus(t, decide(w.is1.c, s, "approve", nil), http.StatusForbidden, "separation_of_duties")
	if inbox := w.inbox(w.is1.c); len(inbox) != 0 {
		t.Fatalf("beneficiary's inbox = %v; want empty", inbox)
	}
	if w.audits("APPROVAL_ATTEMPT_REJECTED") != 1 {
		t.Fatal("the rejected attempt was not recorded")
	}
}

// Approved 2026-10-28: someone who requested on another's behalf decides no step on it.
func TestOnBehalfSubmitterCannotApprove(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.exec("UPDATE tools SET owner_id = $1", w.is2.ID)
	w.exec("INSERT INTO role_assignments (employee_id, role) VALUES ($1, 'HR_MANAGER')", w.is2.ID) // HR may request for anyone
	it := w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.is2.c, body(&w.dev.ID, item(w.awsAdmin))))[0]))
	s := open(t, it)
	if s["pool"] != "INFOSEC" || s["routingNote"] != "tool owner is the submitter → InfoSec" {
		t.Fatalf("Tool Owner step = %v", s)
	}
	testutil.RequireStatus(t, decide(w.is2.c, s, "approve", nil), http.StatusForbidden, "separation_of_duties")
	if inbox := w.inbox(w.is2.c); len(inbox) != 0 {
		t.Fatalf("submitter's inbox = %v; want empty", inbox)
	}
	w.approve(w.is1.c, s)
}

func TestRejectNeedsAReasonAndIsFinal(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	req := w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin), item(w.jiraViewer)))
	aws, jira := items(req)[0], items(req)[1]
	s := open(t, aws)
	testutil.RequireStatus(t, decide(w.mgr.c, s, "reject", nil), http.StatusBadRequest, "field_required")
	testutil.RequireStatus(t, decide(w.mgr.c, s, "reject", map[string]any{"reason": "  "}), http.StatusBadRequest, "reason_required")
	r := decide(w.mgr.c, s, "reject", map[string]any{"reason": "not needed for this role"})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	st := steps(r.Body)
	if r.Body["status"] != "REJECTED" || st[0]["status"] != "REJECTED" || st[0]["reason"] != "not needed for this role" ||
		st[1]["status"] != "CANCELLED" || st[2]["status"] != "CANCELLED" {
		t.Fatalf("item = %v", r.Body)
	}
	if w.audits("STEP_REJECTED") != 1 || w.audits("ITEM_REJECTED") != 1 {
		t.Fatal("rejection not audited")
	}
	// Final: no later decision; the other item is unaffected; a new request is allowed (O5).
	testutil.RequireStatus(t, decide(w.mgr.c, s, "approve", nil), http.StatusConflict, "item_not_pending")
	if it := w.approve(w.mgr.c, open(t, jira)); it["status"] != "APPROVED" {
		t.Fatalf("sibling item = %v", it)
	}
	w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin)))
}

func TestOnlyTheAssigneeDecides(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	s := open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0])
	auditor := w.person(testutil.EmployeeSpec{Roles: []string{"AUDITOR"}})
	hr := w.person(testutil.EmployeeSpec{Roles: []string{"HR_MANAGER"}})
	for _, p := range []person{w.dev, w.skip, w.owner, w.is1, w.admin, auditor, hr} {
		for _, act := range []string{"approve", "reject"} {
			testutil.RequireStatus(t, decide(p.c, s, act, map[string]any{"reason": "x"}), http.StatusForbidden, "forbidden")
		}
	}
	// A later step is routed only when it opens: nobody decides it yet, not even the owner.
	waiting := steps(items(w.get(w.dev.c, "/api/v1/requests").Body["requests"].([]any)[0].(map[string]any))[0])[1]
	testutil.RequireStatus(t, decide(w.owner.c, waiting, "approve", nil), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, decide(w.mgr.c, map[string]any{"id": uuid.NewString(), "version": 1}, "approve", nil), http.StatusNotFound, "not_found")
	if n := w.audits("STEP_APPROVED") + w.audits("STEP_REJECTED"); n != 0 {
		t.Fatalf("unauthorized decisions were applied: %d", n)
	}
	if n := w.audits("APPROVAL_ATTEMPT_REJECTED"); n != 13 { // the Auditor's two never reach the approval (O7 note 2026-10-31)
		t.Fatalf("recorded attempts = %d; want 13", n)
	}
}

func TestStaleVersionIsAConflict(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	s := open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer))))[0])
	s["version"] = 99
	testutil.RequireStatus(t, decide(w.mgr.c, s, "approve", nil), http.StatusConflict, "version_conflict")
}

// Ten InfoSec members decide one pool step at once: one wins, the others are rejected and
// each rejection is recorded although its transaction rolled back.
func TestConcurrentApprovals(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	loner := w.person(testutil.EmployeeSpec{})
	s := open(t, items(w.mustSubmit(loner.c, body(nil, item(w.jiraViewer))))[0])
	members := []person{w.is1, w.is2}
	for len(members) < 10 {
		members = append(members, w.person(testutil.EmployeeSpec{Roles: []string{"INFOSEC"}}))
	}
	var wg sync.WaitGroup
	results := make([]testutil.Response, len(members))
	for i, m := range members {
		wg.Add(1)
		go func() { defer wg.Done(); results[i] = decide(m.c, s, "approve", nil) }()
	}
	wg.Wait()
	won := 0
	for _, r := range results {
		switch {
		case r.Status == http.StatusOK:
			won++
		case r.Status != http.StatusConflict || (r.Body["error"] != "item_not_pending" && r.Body["error"] != "step_already_decided"):
			t.Fatalf("loser = %d %v", r.Status, r.Body)
		}
	}
	if won != 1 || w.audits("STEP_APPROVED") != 1 || w.audits("ITEM_APPROVED") != 1 {
		t.Fatalf("winners = %d; want exactly one decision", won)
	}
	if n := w.audits("APPROVAL_ATTEMPT_REJECTED"); n != 9 {
		t.Fatalf("recorded losing attempts = %d; want 9", n)
	}
}

func TestRepeatedApproveIsIdempotent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	s := open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0])
	first := w.approve(w.mgr.c, s)
	var wg sync.WaitGroup
	for range 5 { // a double-click, also concurrent
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := decide(w.mgr.c, s, "approve", nil); r.Status != http.StatusOK || r.Body["version"] != first["version"] {
				t.Errorf("repeat = %d %v", r.Status, r.Body)
			}
		}()
	}
	wg.Wait()
	if w.audits("STEP_APPROVED") != 1 || w.audits("APPROVAL_ATTEMPT_REJECTED") != 0 {
		t.Fatal("a repeated approval changed something")
	}
	// The opposite decision by the same person is a conflict, not a change.
	testutil.RequireStatus(t, decide(w.mgr.c, s, "reject", map[string]any{"reason": "changed my mind"}), http.StatusConflict, "step_already_decided")
}

func (w *world) inbox(c *testutil.Client) []any {
	w.t.Helper()
	r := w.get(c, "/api/v1/approvals")
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	return r.Body["approvals"].([]any)
}

func TestInboxShowsOnlyMySteps(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin), item(w.jiraViewer)))
	loner := w.person(testutil.EmployeeSpec{})
	w.mustSubmit(loner.c, body(nil, item(w.github))) // Manager step in the InfoSec pool

	mine := w.inbox(w.mgr.c)
	if len(mine) != 2 {
		t.Fatalf("manager's inbox = %v; want both items", mine)
	}
	a := mine[0].(map[string]any)
	it, st := a["item"].(map[string]any), a["step"].(map[string]any)
	if it["beneficiary"].(map[string]any)["id"] != w.dev.ID.String() || it["justification"] != "for the project" ||
		it["duration"].(map[string]any)["type"] != "UNTIL_REVOKED" || st["openedAt"] == nil || len(steps(it)) == 0 {
		t.Fatalf("inbox entry = %v; want beneficiary, justification, duration, steps and waiting time (D6, D9)", a)
	}
	for _, p := range []person{w.dev, w.owner, w.skip, w.admin} {
		if l := w.inbox(p.c); len(l) != 0 {
			t.Fatalf("inbox = %v; want empty", l)
		}
	}
	for _, p := range []person{w.is1, w.is2} {
		if l := w.inbox(p.c); len(l) != 1 || l[0].(map[string]any)["step"].(map[string]any)["pool"] != "INFOSEC" {
			t.Fatalf("InfoSec inbox = %v; want the pool step", l)
		}
	}
}

// A withdrawal committed while the approver waited for the item's lock is seen.
func TestApproveSeesAWithdrawalCommittedWhileWaiting(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	it := items(w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer))))[0]
	stepID := uuid.MustParse(open(t, it)["id"].(string))
	err := testutil.WhileLocked(t, w.db.App, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE request_items SET status = 'WITHDRAWN', version = version + 1 WHERE id = $1", it["id"])
		return err
	}, func() error {
		_, err := w.svc.Approve(context.Background(), w.mgr.principal(), stepID, 2)
		return err
	})
	var e *platform.Error
	if !errors.As(err, &e) || e.Code != "item_not_pending" {
		t.Fatalf("approve = %v; want item_not_pending", err)
	}
	if w.audits("STEP_APPROVED") != 0 {
		t.Fatal("a withdrawn item was approved")
	}
}

// Regression (ENG-005): an override or reassignment re-checks, under the item's lock, that
// the Super Admin was not suspended after their session was checked (D7).
func TestOverrideAndReassignSeeASuspensionCommittedWhileWaiting(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	levels := []uuid.UUID{w.jiraViewer, w.github}
	for name, call := range map[string]func(uuid.UUID) error{
		"override": func(step uuid.UUID) error {
			_, err := w.svc.Override(context.Background(), w.admin.principal("SUPER_ADMIN"), step, 2, "urgent")
			return err
		},
		"reassign": func(step uuid.UUID) error {
			_, err := w.svc.Reassign(context.Background(), w.admin.principal("SUPER_ADMIN"), step, 2, w.is2.ID, "on leave")
			return err
		},
	} {
		level := levels[0]
		levels = levels[1:]
		it := items(w.mustSubmit(w.dev.c, body(nil, item(level))))[0]
		step := uuid.MustParse(open(t, it)["id"].(string))
		w.exec("UPDATE employees SET status = 'ACTIVE' WHERE id = $1", w.admin.ID)
		err := testutil.WhileLocked(t, w.db.App, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "SELECT 1 FROM request_items WHERE id = $1 FOR UPDATE", it["id"]); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "UPDATE employees SET status = 'SUSPENDED' WHERE id = $1", w.admin.ID)
			return err
		}, func() error { return call(step) })
		if !errors.Is(err, platform.ErrUnauthorized) {
			t.Fatalf("%s = %v; want unauthorized", name, err)
		}
	}
}
