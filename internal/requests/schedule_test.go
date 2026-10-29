package requests

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/sweeps"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func (w *world) schedule(by person, id uuid.UUID, at any) testutil.Response {
	w.t.Helper()
	return by.c.Do(http.MethodPut, "/api/v1/employees/"+id.String()+"/termination",
		map[string]any{"effectiveAt": at, "reason": "leaving", "version": w.version(id)})
}

func (w *world) cancelSchedule(by person, id uuid.UUID) testutil.Response {
	w.t.Helper()
	return by.c.Do(http.MethodPost, "/api/v1/employees/"+id.String()+"/termination/cancel",
		map[string]any{"reason": "staying", "version": w.version(id)})
}

func inDays(n int) string {
	return time.Now().Add(time.Duration(n) * 24 * time.Hour).UTC().Format(time.RFC3339)
}

// makeDue moves a scheduled termination into the past (D12: dates move, the clock does not).
func (w *world) makeDue(id uuid.UUID) {
	w.exec("UPDATE employees SET termination_at = now() - interval '1 second' WHERE id = $1", id)
}

func TestScheduleTermination(t *testing.T) {
	w := newWorld(t)
	hr := w.hr()
	r := w.schedule(hr, w.dev.ID, inDays(7))
	testutil.RequireStatus(t, r, http.StatusOK, "")
	term, _ := r.Body["termination"].(map[string]any)
	if r.Body["status"] != "ACTIVE" || term == nil || term["reason"] != "leaving" || term["scheduledBy"] != hr.ID.String() {
		t.Fatalf("employee = %v", r.Body)
	}
	testutil.RequireStatus(t, w.schedule(hr, w.dev.ID, inDays(14)), http.StatusOK, "")
	if w.audits("TERMINATION_SCHEDULED") != 1 || w.count(`SELECT count(*) FROM audit_events WHERE action = 'TERMINATION_RESCHEDULED'
		AND details ? 'from' AND details ? 'to'`) != 1 {
		t.Fatal("scheduling not audited")
	}
	r = w.cancelSchedule(hr, w.dev.ID)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["termination"] != nil || w.audits("TERMINATION_CANCELLED") != 1 {
		t.Fatalf("employee = %v", r.Body)
	}
	testutil.RequireStatus(t, w.cancelSchedule(hr, w.dev.ID), http.StatusConflict, "termination_not_scheduled")

	testutil.RequireStatus(t, w.schedule(hr, w.dev.ID, inDays(-1)), http.StatusBadRequest, "effective_at_not_future")
	testutil.RequireStatus(t, w.schedule(hr, w.dev.ID, "next friday"), http.StatusBadRequest, "")
	testutil.RequireStatus(t, w.schedule(hr, w.dev.ID, nil), http.StatusBadRequest, "")
	testutil.RequireStatus(t, w.schedule(w.owner, w.dev.ID, inDays(7)), http.StatusForbidden, "")
	testutil.RequireStatus(t, w.schedule(hr, hr.ID, inDays(7)), http.StatusForbidden, "self_change_forbidden")
	testutil.RequireStatus(t, w.schedule(hr, w.is1.ID, inDays(7)), http.StatusForbidden, "privileged_reset_requires_super_admin")
	testutil.RequireStatus(t, w.cancelSchedule(w.owner, w.dev.ID), http.StatusForbidden, "")
	w.mustLifecycle(hr, w.dev.ID, "suspend")
	testutil.RequireStatus(t, w.schedule(hr, w.dev.ID, inDays(7)), http.StatusOK, "") // a suspended employee can be scheduled
	w.mustLifecycle(hr, w.dev.ID, "terminate")
	testutil.RequireStatus(t, w.schedule(hr, w.dev.ID, inDays(7)), http.StatusConflict, "employee_status")

	// Both Super Admins may be scheduled to leave: the last one leaving starts recovery (D2, M14).
	other := w.person(testutil.EmployeeSpec{Roles: []string{"SUPER_ADMIN"}})
	testutil.RequireStatus(t, w.schedule(w.admin, other.ID, inDays(7)), http.StatusOK, "")
	testutil.RequireStatus(t, w.schedule(other, w.admin.ID, inDays(7)), http.StatusOK, "")
}

func TestScheduledTerminationRuns(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	hr := w.hr()
	w.approved(w.jiraViewer)
	tl.drain(t)
	leaver := w.person(testutil.EmployeeSpec{ManagerID: w.mgr.ID})
	testutil.RequireStatus(t, w.schedule(hr, w.dev.ID, inDays(7)), http.StatusOK, "")
	testutil.RequireStatus(t, w.schedule(hr, leaver.ID, inDays(7)), http.StatusOK, "")
	w.mustLifecycle(hr, leaver.ID, "suspend")
	w.sweep()
	if w.count("SELECT count(*) FROM employees WHERE status = 'TERMINATED'") != 0 {
		t.Fatal("terminated before its time")
	}
	w.makeDue(w.dev.ID)
	w.makeDue(leaver.ID)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() { errs <- sweeps.New(w.db.App, w.svc.loc, TerminateDue).RunOnce(context.Background()) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	w.sweep()
	if w.count("SELECT count(*) FROM employees WHERE id IN ($1, $2) AND status = 'TERMINATED' AND termination_at IS NULL", w.dev.ID, leaver.ID) != 2 {
		t.Fatal("not terminated")
	}
	if w.count(`SELECT count(*) FROM audit_events WHERE action = 'EMPLOYEE_TERMINATED' AND actor_id IS NULL
		AND details->>'scheduledBy' = $1 AND reason = 'leaving'`, hr.ID.String()) != 2 || w.audits("EMPLOYEE_TERMINATED") != 2 {
		t.Fatal("termination not audited once each, by the system")
	}
	if w.count(`SELECT count(*) FROM operations o JOIN grants g ON g.id = o.grant_id
		WHERE g.employee_id = $1 AND o.type = 'REVOKE' AND o.priority = 0`, w.dev.ID) != 1 {
		t.Fatal("one removal expected")
	}
}

func TestImmediateTerminationOverridesSchedule(t *testing.T) {
	w := newWorld(t)
	hr := w.hr()
	testutil.RequireStatus(t, w.schedule(hr, w.dev.ID, inDays(7)), http.StatusOK, "")
	e := w.mustLifecycle(hr, w.dev.ID, "terminate")
	if e["termination"] != nil {
		t.Fatalf("schedule kept: %v", e)
	}
	w.sweep()
	if w.audits("EMPLOYEE_TERMINATED") != 1 {
		t.Fatal("terminated twice")
	}
}

// The sweep lists the employee, then waits for the lifecycle lock while a cancellation
// commits: its re-check under the lock sees no schedule and does nothing.
func TestScheduleCancelledWhileSweeping(t *testing.T) {
	w := newWorld(t)
	hr := w.hr()
	testutil.RequireStatus(t, w.schedule(hr, w.dev.ID, inDays(7)), http.StatusOK, "")
	w.makeDue(w.dev.ID)
	err := testutil.WhileLocked(t, w.db.App, func(ctx context.Context, tx pgx.Tx) error {
		if err := people.LockLifecycle(ctx, tx); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE employees SET termination_at = NULL, termination_scheduled_by = NULL, termination_reason = NULL WHERE id = $1", w.dev.ID)
		return err
	}, func() error {
		_, err := TerminateDue(context.Background(), w.db.App)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.count("SELECT count(*) FROM employees WHERE id = $1 AND status = 'ACTIVE'", w.dev.ID) != 1 {
		t.Fatal("terminated after the cancellation")
	}
}
