package requests

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func (w *world) rehire(by person, id uuid.UUID, manager any, version int) testutil.Response {
	w.t.Helper()
	return by.c.Do(http.MethodPost, "/api/v1/employees/"+id.String()+"/rehire", map[string]any{"version": version,
		"managerId": manager, "department": "Platform", "jobTitle": "Engineer", "reason": "Rehired, case 9"})
}

// A rehired employee is the same person with a new password and nothing restored (A5, D7).
func TestRehireRestoresNothing(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	f := w.fulfillers()
	w.approved(w.jiraViewer)
	tl.drain(t)
	w.fulfilled(f, f.manual)
	w.exec("INSERT INTO role_assignments (employee_id, role) VALUES ($1, 'AUDITOR')", w.dev.ID)
	w.member(w.dev, f.vendorTool)
	pending := items(w.mustSubmit(w.dev.c, body(nil, item(w.github))))[0]
	hr := w.hr()
	w.mustLifecycle(hr, w.dev.ID, "terminate")
	tl.drain(t)
	w.mustAct(f.fixer, w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "REVOKE"), "claim", nil), "confirm", nil)
	v := w.version(w.dev.ID)

	r := w.rehire(hr, w.dev.ID, w.mgr.ID, v)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	e := r.Body["employee"].(map[string]any)
	if e["id"] != w.dev.ID.String() || e["status"] != "ACTIVE" || e["version"] != float64(v+1) || e["department"] != "Platform" ||
		e["managerId"] != w.mgr.ID.String() || e["termination"] != nil {
		t.Fatalf("employee = %v", e)
	}
	c := testutil.NewClient(t, w.srv)
	login := c.Login(w.dev.Email, r.Body["temporaryPassword"].(string))
	testutil.RequireStatus(t, login, http.StatusOK, "")
	if login.Body["mustChangePassword"] != true {
		t.Fatalf("login = %v", login.Body)
	}
	if w.count("SELECT count(*) FROM grants WHERE employee_id = $1 AND status NOT IN ('REVOKED', 'CANCELLED')", w.dev.ID) != 0 ||
		w.count("SELECT count(*) FROM role_assignments WHERE employee_id = $1 AND valid_to IS NULL", w.dev.ID) != 0 ||
		w.count("SELECT count(*) FROM admin_group_memberships WHERE employee_id = $1 AND valid_to IS NULL", w.dev.ID) != 0 {
		t.Fatal("a rehire restored access")
	}
	if w.itemStatus(pending) != "CANCELLED:employee terminated" || w.audits("EMPLOYEE_REHIRED") != 1 {
		t.Fatalf("item %s, EMPLOYEE_REHIRED %d", w.itemStatus(pending), w.audits("EMPLOYEE_REHIRED"))
	}
	testutil.RequireStatus(t, c.Do(http.MethodPost, "/api/v1/auth/password",
		map[string]string{"currentPassword": r.Body["temporaryPassword"].(string), "newPassword": "a brand new password"}), http.StatusOK, "")
	it := items(w.mustSubmit(c, body(nil, item(w.jiraViewer))))[0]
	if s := open(t, it); s["assigneeId"] != w.mgr.ID.String() {
		t.Fatalf("new request's step = %v", s)
	}
}

func TestRehireRules(t *testing.T) {
	w := newWorld(t)
	hr := w.hr()
	testutil.RequireStatus(t, w.rehire(hr, w.dev.ID, w.mgr.ID, w.version(w.dev.ID)), http.StatusConflict, "employee_status")
	w.mustLifecycle(hr, w.mgr.ID, "terminate") // dev reported to mgr
	testutil.RequireStatus(t, w.rehire(w.owner, w.mgr.ID, nil, w.version(w.mgr.ID)), http.StatusForbidden, "")
	gone := w.person(testutil.EmployeeSpec{})
	w.mustLifecycle(hr, gone.ID, "suspend")
	testutil.RequireStatus(t, w.rehire(hr, w.mgr.ID, gone.ID, w.version(w.mgr.ID)), http.StatusConflict, "has_direct_reports")
	testutil.RequireStatus(t, w.transfer(hr, w.dev.ID, w.skip.ID, "Engineering"), http.StatusOK, "") // HR moves the report first
	testutil.RequireStatus(t, w.rehire(hr, w.mgr.ID, gone.ID, w.version(w.mgr.ID)), http.StatusBadRequest, "manager_inactive")
	testutil.RequireStatus(t, w.rehire(hr, w.mgr.ID, nil, w.version(w.mgr.ID)-1), http.StatusConflict, "version_conflict")
	testutil.RequireStatus(t, w.rehire(hr, hr.ID, nil, w.version(hr.ID)), http.StatusForbidden, "self_change_forbidden")

	v := w.version(w.mgr.ID)
	err := testutil.WhileLocked(t, w.db.App, func(ctx context.Context, tx pgx.Tx) error {
		if err := people.LockLifecycle(ctx, tx); err != nil { // as a suspension does
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE employees SET status = 'SUSPENDED' WHERE id = $1", hr.ID)
		return err
	}, func() error {
		if r := w.rehire(hr, w.mgr.ID, nil, v); r.Status != http.StatusUnauthorized {
			return fmt.Errorf("status %d %v; want 401", r.Status, r.Body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.audits("EMPLOYEE_REHIRED") != 0 {
		t.Fatal("a refused rehire was recorded")
	}
}

// A rehired manager never takes back their former reports: HR transfers them first (A5, 2026-10-29).
func TestRehireRefusedWithDirectReports(t *testing.T) {
	w := newWorld(t)
	hr := w.hr()
	leaver := w.person(testutil.EmployeeSpec{ManagerID: w.mgr.ID})
	w.mustLifecycle(hr, leaver.ID, "terminate") // a former report who left does not count
	w.mustLifecycle(hr, w.mgr.ID, "terminate")  // dev still reports to mgr
	r := w.rehire(hr, w.mgr.ID, w.skip.ID, w.version(w.mgr.ID))
	testutil.RequireStatus(t, r, http.StatusConflict, "has_direct_reports")
	if w.count("SELECT count(*) FROM employees WHERE id = $1 AND status = 'TERMINATED'", w.mgr.ID) != 1 || w.audits("EMPLOYEE_REHIRED") != 0 {
		t.Fatal("refused rehire changed something")
	}
	testutil.RequireStatus(t, w.transfer(hr, w.dev.ID, w.skip.ID, "Engineering"), http.StatusOK, "")
	testutil.RequireStatus(t, w.rehire(hr, w.mgr.ID, w.skip.ID, w.version(w.mgr.ID)), http.StatusOK, "")
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'EMPLOYEE_REHIRED' AND details->'from'->>'department' IS NULL
		AND details->'to'->>'department' = 'Platform' AND details->'to'->>'managerId' = $1`, w.skip.ID); n != 1 {
		t.Fatalf("EMPLOYEE_REHIRED with from and to = %d", n)
	}
}

// Leaver removals keep running and stay visible after a rehire; other removals are not
// leaver removals (D7, O7).
func TestRehireKeepsLeaverRemovalsVisible(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	g := w.fulfilled(f, f.manual)
	hr := w.hr()
	w.mustLifecycle(hr, w.dev.ID, "terminate")
	testutil.RequireStatus(t, w.rehire(hr, w.dev.ID, w.mgr.ID, w.version(w.dev.ID)), http.StatusOK, "")
	if n := len(w.securityRevocations(w.is1)); n != 1 {
		t.Fatalf("security revocations after the rehire = %d", n)
	}
	w.mustAct(f.fixer, w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "REVOKE"), "claim", nil), "confirm", nil)
	if w.grantStatus(g) != "REVOKED" || len(w.securityRevocations(w.is1)) != 0 {
		t.Fatalf("grant %s, listed %d", w.grantStatus(g), len(w.securityRevocations(w.is1)))
	}

	w2 := newWorld(t)
	f2 := w2.fulfillers()
	w2.mustRevoke(w2.dev.c, w2.fulfilled(f2, f2.manual))
	if n := len(w2.securityRevocations(w2.is1)); n != 0 {
		t.Fatalf("an ordinary removal is listed as a leaver's: %d", n)
	}
}
