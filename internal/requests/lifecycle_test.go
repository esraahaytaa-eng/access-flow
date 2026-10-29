package requests

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// lifecycle posts a person-level action (suspend, reactivate, terminate) for id at its
// current version.
func (w *world) lifecycle(by person, id uuid.UUID, action string) testutil.Response {
	w.t.Helper()
	return by.c.Do(http.MethodPost, "/api/v1/employees/"+id.String()+"/"+action,
		map[string]any{"reason": "HR case 42", "version": w.version(id)})
}

func (w *world) mustLifecycle(by person, id uuid.UUID, action string) map[string]any {
	w.t.Helper()
	r := w.lifecycle(by, id, action)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	return r.Body
}

func (w *world) version(id uuid.UUID) int {
	w.t.Helper()
	return w.count("SELECT version FROM employees WHERE id = $1", id)
}

func (w *world) hr() person {
	w.t.Helper()
	return w.person(testutil.EmployeeSpec{Roles: []string{"HR_MANAGER"}})
}

// teamGrant is dev's grant for level as their manager sees it: a suspended dev cannot sign in.
func (w *world) teamGrant(level uuid.UUID) map[string]any {
	w.t.Helper()
	r := w.get(w.mgr.c, "/api/v1/access/team")
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	for _, g := range r.Body["grants"].([]any) {
		if g := g.(map[string]any); g["accessLevelId"] == level.String() {
			return g
		}
	}
	w.t.Fatalf("no team grant for %v in %v", level, r.Body)
	return nil
}

// tool is the mock's state for the grant's level: ACTIVE, SUSPENDED, or "" when not held.
func (tl *tools) state(t *testing.T, g map[string]any, email string) string {
	t.Helper()
	var s string
	err := tl.db.QueryRow(context.Background(), "SELECT status FROM permissions WHERE tool = $1 AND account = $2 AND level = $3",
		g["toolId"], email, g["accessLevelId"]).Scan(&s)
	if err != nil && err != pgx.ErrNoRows {
		t.Fatal(err)
	}
	return s
}

// inFlight starts one runner round whose tool call is delayed, and returns once the
// operation is IN_PROGRESS; the channel yields the round's error.
func (w *world) inFlight(tl *tools) <-chan error {
	w.t.Helper()
	tl.faults.DelayNext(500 * time.Millisecond)
	done := make(chan error, 1)
	go func() { _, err := tl.runner.RunOnce(context.Background()); done <- err }()
	for deadline := time.Now().Add(5 * time.Second); w.count("SELECT count(*) FROM operations WHERE status = 'IN_PROGRESS'") == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			w.t.Fatal("never claimed")
		}
	}
	return done
}

func (w *world) ops(g map[string]any, typ, status string) int {
	w.t.Helper()
	return w.count("SELECT count(*) FROM operations WHERE grant_id = $1 AND type = $2 AND status = $3", g["id"], typ, status)
}

func (w *world) openIntervals(g map[string]any) int {
	w.t.Helper()
	return w.count("SELECT count(*) FROM grant_suspensions WHERE grant_id = $1 AND effective_to IS NULL", g["id"])
}

func TestSuspendAPI(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	hr := w.hr()
	w.approved(w.jiraViewer)
	tl.drain(t)
	w.mustSubmit(w.dev.c, body(nil, item(w.github))) // pending
	before := w.version(w.dev.ID)

	e := w.mustLifecycle(hr, w.dev.ID, "suspend")
	if e["status"] != "SUSPENDED" || int(e["version"].(float64)) != before+1 {
		t.Fatalf("employee = %v", e)
	}
	testutil.RequireStatus(t, w.get(w.dev.c, "/api/v1/access"), http.StatusUnauthorized, "")
	testutil.RequireStatus(t, testutil.NewClient(t, w.srv).Login(w.dev.Email, testutil.Password), http.StatusUnauthorized, "invalid_credentials")
	if w.count(`SELECT count(*) FROM request_items WHERE beneficiary_id = $1 AND status = 'CANCELLED' AND closed_reason = 'employee suspended'`,
		w.dev.ID) != 1 {
		t.Fatal("the pending item was not cancelled")
	}
	g := w.teamGrant(w.jiraViewer)
	if g["status"] != "ACTIVE" || g["suspension"] != "SUSPENSION_REQUESTED" ||
		w.count("SELECT count(*) FROM operations WHERE grant_id = $1 AND type = 'SUSPEND' AND priority = 1 AND status = 'PENDING'", g["id"]) != 1 {
		t.Fatalf("grant before the runner = %v", g)
	}
	tl.drain(t)
	g = w.teamGrant(w.jiraViewer)
	requireGrant(t, g, "SUSPENDED", "SUSPENDED / VERIFIED")
	if g["suspension"] != nil || w.openIntervals(g) != 1 || tl.state(t, g, w.dev.Email) != "SUSPENDED" {
		t.Fatalf("suspension %v, intervals %d, tool %q", g["suspension"], w.openIntervals(g), tl.state(t, g, w.dev.Email))
	}
	if w.count(`SELECT count(*) FROM audit_events WHERE action = 'EMPLOYEE_SUSPENDED' AND actor_id = $1 AND entity_id = $2 AND reason = 'HR case 42'`,
		hr.ID, w.dev.ID) != 1 || w.audits("GRANT_SUSPENDED") != 1 {
		t.Fatal("suspension not audited")
	}
}

func TestSuspendDuringProvisioning(t *testing.T) {
	t.Run("never sent: cancelled", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer) // queued, not sent
		w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
		tl.drain(t)
		g := w.teamGrant(w.jiraViewer)
		if g["status"] != "CANCELLED" || g["closedReason"] != "employee suspended" || tl.state(t, g, w.dev.Email) != "" {
			t.Fatalf("grant = %v", g)
		}
	})
	t.Run("in flight: suspended once active", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer)
		done := w.inFlight(tl)
		w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if g := w.teamGrant(w.jiraViewer); g["status"] != "ACTIVE" || g["suspension"] != "SUSPENSION_REQUESTED" {
			t.Fatalf("activated grant = %v", g)
		}
		tl.drain(t)
		g := w.teamGrant(w.jiraViewer)
		requireGrant(t, g, "SUSPENDED", "SUSPENDED / VERIFIED")
		if tl.state(t, g, w.dev.Email) != "SUSPENDED" {
			t.Fatal("the tool still has the level active")
		}
	})
}

func TestSuspendDuringLevelChange(t *testing.T) {
	t.Run("new grant never sent", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.levelChange(tl)
		w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
		tl.drain(t)
		niu, old := w.teamGrant(w.jiraAdmin), w.teamGrant(w.jiraViewer)
		if niu["status"] != "CANCELLED" {
			t.Fatalf("new grant = %v", niu)
		}
		requireGrant(t, old, "SUSPENDED", "SUSPENDED / VERIFIED") // D4: the old level is kept
		if tl.state(t, niu, w.dev.Email) != "" || tl.state(t, old, w.dev.Email) != "SUSPENDED" {
			t.Fatal("tool state")
		}
	})
	t.Run("new grant in flight", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.levelChange(tl)
		done := w.inFlight(tl)
		w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		tl.drain(t)
		niu, old := w.teamGrant(w.jiraAdmin), w.teamGrant(w.jiraViewer)
		requireGrant(t, niu, "SUSPENDED", "SUSPENDED / VERIFIED")
		requireGrant(t, old, "REVOKED", "REVOKED / VERIFIED")
		if tl.state(t, niu, w.dev.Email) != "SUSPENDED" || tl.state(t, old, w.dev.Email) != "" || w.openIntervals(old) != 0 {
			t.Fatal("want only the new level, suspended")
		}
	})
}

func TestExpiryAndRevokeDuringSuspension(t *testing.T) {
	t.Run("expiry of a suspended grant", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
		tl.drain(t)
		g := w.teamGrant(w.jiraViewer)
		w.expiresOn(g, in(-1))
		w.sweep()
		tl.drain(t)
		g = w.teamGrant(w.jiraViewer)
		requireGrant(t, g, "REVOKED", "REVOKED / VERIFIED")
		if w.openIntervals(g) != 0 || tl.state(t, g, w.dev.Email) != "" {
			t.Fatal("interval left open or level still held")
		}
	})
	t.Run("revoke while the SUSPEND is queued", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
		g := w.mustRevoke(w.mgr.c, w.teamGrant(w.jiraViewer))
		if w.ops(g, "SUSPEND", "CANCELLED") != 1 {
			t.Fatal("the queued SUSPEND was not cancelled")
		}
		tl.drain(t)
		requireGrant(t, w.teamGrant(w.jiraViewer), "REVOKED", "REVOKED / VERIFIED")
	})
	t.Run("revoke while the SUSPEND is in flight", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
		done := w.inFlight(tl)
		g := w.mustRevoke(w.mgr.c, w.teamGrant(w.jiraViewer))
		if found, err := tl.runner.RunOnce(context.Background()); err != nil || found {
			t.Fatalf("the REVOKE ran before the SUSPEND in flight finished (found %v, %v)", found, err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		tl.drain(t)
		g = w.teamGrant(w.jiraViewer)
		requireGrant(t, g, "REVOKED", "REVOKED / VERIFIED")
		if w.openIntervals(g) != 0 || w.ops(g, "SUSPEND", "SUCCEEDED") != 1 {
			t.Fatal("the suspension in the tool was not recorded and closed")
		}
	})
}

func TestSuspendReroutesSteps(t *testing.T) {
	w := newWorld(t)
	req := w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer)))
	step := open(t, items(req)[0])
	if step["assigneeId"] != w.mgr.ID.String() {
		t.Fatalf("step = %v", step)
	}
	w.mustLifecycle(w.hr(), w.mgr.ID, "suspend")
	var assignee uuid.UUID
	var note string
	if err := w.db.App.QueryRow(context.Background(), "SELECT assignee_id, routing_note FROM approval_steps WHERE id = $1", step["id"]).
		Scan(&assignee, &note); err != nil {
		t.Fatal(err)
	}
	if assignee != w.skip.ID || note != "manager inactive → manager's manager" || w.audits("STEP_REROUTED") != 1 {
		t.Fatalf("assignee %v, note %q", assignee, note)
	}
	testutil.RequireStatus(t, decide(w.mgr.c, step, "approve", nil), http.StatusUnauthorized, "")
	w.approve(w.skip.c, open(t, items(w.get(w.dev.c, "/api/v1/requests/"+req["id"].(string)).Body)[0]))
}

// An approval that passed the session check before its approver's suspension committed
// is refused afterwards. An assigned step's item is in the suspension's lock set, so the
// two serialize; a pool step's item is not, so the approval itself must check its actor.
func TestApproveWhileApproverIsSuspended(t *testing.T) {
	for _, pool := range []bool{false, true} {
		t.Run(map[bool]string{false: "assigned step", true: "InfoSec pool step"}[pool], func(t *testing.T) {
			w := newWorld(t)
			level, approver := w.jiraViewer, w.mgr
			if pool {
				level, approver = w.awsAdmin, w.is1 // Manager, Tool Owner, then InfoSec
			}
			it := items(w.mustSubmit(w.dev.c, body(nil, item(level))))[0]
			if pool {
				it = w.approve(w.owner.c, open(t, w.approve(w.mgr.c, open(t, it))))
			}
			step := open(t, it)
			hr := w.hr()
			if pool {
				hr = w.admin // an InfoSec member is suspended only by a Super Admin
			}
			ctx := context.Background()
			tx, err := w.db.App.Begin(ctx) // holds the item: the approval waits after its session check
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "SELECT 1 FROM request_items WHERE id = $1 FOR UPDATE", it["id"]); err != nil {
				t.Fatal(err)
			}
			decided := make(chan testutil.Response, 1)
			go func() { decided <- decide(approver.c, step, "approve", nil) }()
			w.waitForLock()
			suspended := make(chan testutil.Response, 1)
			go func() { suspended <- w.lifecycle(hr, approver.ID, "suspend") }()
			if pool { // nothing holds the suspension up: let it commit first
				testutil.RequireStatus(t, <-suspended, http.StatusOK, "")
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			r := <-decided
			if !pool {
				testutil.RequireStatus(t, <-suspended, http.StatusOK, "")
			}
			// Event IDs are uuidv7, ordered by insertion time; the transactions' times are not.
			late := w.count(`SELECT count(*) FROM audit_events a WHERE a.action = 'STEP_APPROVED' AND a.actor_id = $1 AND a.id >
				(SELECT id FROM audit_events WHERE action = 'EMPLOYEE_SUSPENDED' AND entity_id = $1)`, approver.ID)
			if late != 0 {
				t.Fatalf("a decision by the suspended person was recorded after the suspension (approve %d)", r.Status)
			}
			if pool {
				testutil.RequireStatus(t, r, http.StatusUnauthorized, "")
			}
		})
	}
}

// waitForLock returns once some statement in this test's database waits for a row lock.
func (w *world) waitForLock() {
	w.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); w.count(`SELECT count(*) FROM pg_stat_activity
		WHERE datname = current_database() AND wait_event_type = 'Lock'`) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			w.t.Fatal("nothing waited for a lock")
		}
	}
}

// A step re-routed to someone whose suspension is committing never stays with them.
func TestRerouteToSomeoneBeingSuspended(t *testing.T) {
	w := newWorld(t)
	w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer))) // the Manager step is mgr's
	hr := w.hr()
	err := testutil.WhileLocked(t, w.db.App, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE employees SET status = 'SUSPENDED' WHERE id = $1", w.skip.ID) // skip's suspension, committing
		return err
	}, func() error {
		r := w.lifecycle(hr, w.mgr.ID, "suspend") // re-routes mgr's step to skip
		if r.Status != http.StatusOK {
			t.Errorf("suspend = %d %v", r.Status, r.Body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM approval_steps WHERE status = 'PENDING' AND assignee_id = $1`, w.skip.ID); n != 0 {
		t.Fatal("the step went to a suspended manager's manager")
	}
}

// A step routed to the person after the action locked its items, and withdrawn while the
// action re-routes, is left as the withdrawal closed it (review 2026-10-29, finding 5).
func TestRerouteSkipsAStepClosedMeanwhile(t *testing.T) {
	w := newWorld(t)
	it := items(w.mustSubmit(w.mgr.c, body(nil, item(w.jiraViewer))))[0] // the Manager step is skip's
	hr := w.hr()
	ctx := context.Background()
	hold, err := w.db.App.Begin(ctx) // holds dev after the action's item locks
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(ctx)
	if _, err := hold.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR KEY SHARE", w.dev.ID); err != nil {
		t.Fatal(err)
	}
	suspended := make(chan testutil.Response, 1)
	go func() { suspended <- w.lifecycle(hr, w.dev.ID, "suspend") }()
	w.waitForLock()
	w.exec("UPDATE approval_steps SET assignee_id = $1 WHERE item_id = $2 AND status = 'PENDING'", w.dev.ID, it["id"]) // routed to dev now
	// mgr's withdrawal, committing
	withdrawal, err := w.db.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer withdrawal.Rollback(ctx)
	for _, q := range []string{
		"SELECT 1 FROM request_items WHERE id = $1 FOR UPDATE",
		"UPDATE request_items SET status = 'WITHDRAWN', closed_at = now(), version = version + 1 WHERE id = $1",
		"UPDATE approval_steps SET status = 'CANCELLED', version = version + 1 WHERE item_id = $1 AND status IN ('WAITING', 'PENDING')",
	} {
		if _, err := withdrawal.Exec(ctx, q, it["id"]); err != nil {
			t.Fatal(err)
		}
	}
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); w.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
		AND wait_event_type = 'Lock' AND query LIKE '%request_items WHERE id = $1 FOR UPDATE%'`) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the re-route never waited for the item")
		}
	}
	if err := withdrawal.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	testutil.RequireStatus(t, <-suspended, http.StatusOK, "")
	if w.audits("STEP_REROUTED") != 0 || w.count(`SELECT count(*) FROM approval_steps WHERE item_id = $1 AND status = 'CANCELLED'
		AND assignee_id = $2`, it["id"], w.dev.ID) != 1 {
		t.Fatal("the withdrawn item's step was re-routed")
	}
}

func TestSuspendPermissions(t *testing.T) {
	w := newWorld(t)
	hr := w.hr()
	plain := w.lifecycle(w.owner, w.dev.ID, "suspend")
	testutil.RequireStatus(t, plain, http.StatusForbidden, "")
	testutil.RequireStatus(t, w.lifecycle(hr, hr.ID, "suspend"), http.StatusForbidden, "self_change_forbidden")
	testutil.RequireStatus(t, w.lifecycle(hr, w.is1.ID, "suspend"), http.StatusForbidden, "privileged_reset_requires_super_admin")
	testutil.RequireStatus(t, w.lifecycle(w.admin, w.is1.ID, "suspend"), http.StatusOK, "")
	testutil.RequireStatus(t, w.lifecycle(hr, w.is1.ID, "suspend"), http.StatusForbidden, "privileged_reset_requires_super_admin")
	testutil.RequireStatus(t, w.lifecycle(w.admin, w.is1.ID, "suspend"), http.StatusConflict, "employee_status")
	testutil.RequireStatus(t, hr.c.Do(http.MethodPost, "/api/v1/employees/"+w.dev.ID.String()+"/suspend",
		map[string]any{"reason": "x", "version": w.version(w.dev.ID) + 1}), http.StatusConflict, "version_conflict")
	testutil.RequireStatus(t, hr.c.Do(http.MethodPost, "/api/v1/employees/"+w.dev.ID.String()+"/suspend",
		map[string]any{"reason": " ", "version": w.version(w.dev.ID)}), http.StatusBadRequest, "reason_required")
	testutil.RequireStatus(t, hr.c.Do(http.MethodPost, "/api/v1/employees/"+uuid.NewString()+"/suspend",
		map[string]any{"reason": "x", "version": 1}), http.StatusNotFound, "")
}

func TestSuspendConcurrently(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	hr, hr2 := w.hr(), w.hr()
	v := w.version(w.dev.ID)
	var wg sync.WaitGroup
	got := make([]int, 2)
	for i, p := range []person{hr, hr2} {
		wg.Go(func() {
			got[i] = p.c.Do(http.MethodPost, "/api/v1/employees/"+w.dev.ID.String()+"/suspend", map[string]any{"reason": "x", "version": v}).Status
		})
	}
	wg.Wait()
	if !(got[0] == 200 && got[1] == 409 || got[0] == 409 && got[1] == 200) {
		t.Fatalf("statuses = %v", got)
	}
	if n := w.count("SELECT count(*) FROM operations WHERE type = 'SUSPEND'"); n != 1 || w.audits("EMPLOYEE_SUSPENDED") != 1 {
		t.Fatalf("SUSPEND operations = %d", n)
	}
}

// The runner records a grant's activation while the employee is being suspended: both
// take the employee first, so neither deadlocks and the grant ends suspended.
func TestSuspendWhileRunnerRecords(t *testing.T) {
	for round := range 10 {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer)
		var wg sync.WaitGroup
		var suspendErr testutil.Response
		var runErr error
		wg.Go(func() { _, runErr = tl.runner.RunOnce(context.Background()) })
		wg.Go(func() { suspendErr = w.lifecycle(w.hr(), w.dev.ID, "suspend") })
		wg.Wait()
		if runErr != nil || suspendErr.Status != http.StatusOK {
			t.Fatalf("round %d: runner %v, suspend %d %v", round, runErr, suspendErr.Status, suspendErr.Body)
		}
		tl.drain(t)
		g := w.teamGrant(w.jiraViewer)
		if g["status"] != "CANCELLED" && g["status"] != "SUSPENDED" {
			t.Fatalf("round %d: grant %v", round, g["status"])
		}
	}
}

// gaps lists Exceptions' suspensionGaps as p sees them.
func (w *world) gaps(p person) []any {
	w.t.Helper()
	r := w.exceptions(p)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	return r.Body["suspensionGaps"].([]any)
}

// due makes every queued operation due now (D12: dates move, the clock does not).
func (w *world) due() {
	w.t.Helper()
	w.exec("UPDATE operations SET next_attempt_at = now() WHERE status = 'PENDING'")
}

func TestSuspendManualAndVendor(t *testing.T) {
	for _, method := range []string{"MANUAL", "VENDOR"} {
		t.Run(method, func(t *testing.T) {
			w := newWorld(t)
			f := w.fulfillers()
			level := map[string]uuid.UUID{"MANUAL": f.manual, "VENDOR": f.vendor}[method]
			w.fulfilled(f, level)
			w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
			if g := w.teamGrant(level); g["status"] != "ACTIVE" || g["suspension"] != "SUSPENSION_REQUESTED" {
				t.Fatalf("grant = %v", g)
			}
			task := w.mustAct(f.fixer, w.mustTask(f.fixer, level, "SUSPEND"), "claim", nil)
			if method == "VENDOR" {
				testutil.RequireStatus(t, act(f.fixer, task, "confirm", nil), http.StatusConflict, "ticket_required")
				task = w.mustAct(f.fixer, task, "ticket", map[string]any{"ticketRef": "VND-9"})
			}
			w.mustAct(f.fixer, task, "confirm", nil)
			g := w.teamGrant(level)
			requireGrant(t, g, "SUSPENDED", "SUSPENDED / SELF_REPORTED")
			if w.openIntervals(g) != 1 || w.count(`SELECT count(*) FROM grant_suspensions WHERE grant_id = $1 AND evidence = 'SELF_REPORTED'`, g["id"]) != 1 ||
				w.count(`SELECT count(*) FROM audit_events WHERE action = 'GRANT_SUSPENDED' AND actor_id = $1`, f.fixer.ID) != 1 {
				t.Fatal("the confirmed suspension was not recorded with its admin")
			}
		})
	}
}

// The employee is suspended but the tool still grants access: listed from the first failed
// attempt until the suspension is verified or confirmed, and retryable (D7, O7).
func TestSuspensionGapIsVisibleAndActionable(t *testing.T) {
	t.Run("API retrying, then verified", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
		if len(w.gaps(w.is1)) != 0 {
			t.Fatal("a SUSPEND not yet attempted is ordinary work")
		}
		tl.faults.FailNext(1, http.StatusServiceUnavailable)
		tl.drain(t)
		gaps := w.gaps(w.is1)
		if len(gaps) != 1 || gaps[0].(map[string]any)["type"] != "SUSPEND" || gaps[0].(map[string]any)["status"] != "PENDING" {
			t.Fatalf("gaps = %v", gaps)
		}
		if g := w.teamGrant(w.jiraViewer); g["status"] != "ACTIVE" || g["suspension"] != "SUSPENSION_FAILED" {
			t.Fatalf("grant = %v", g)
		}
		auditor := w.person(testutil.EmployeeSpec{Roles: []string{"AUDITOR"}})
		if len(w.gaps(auditor)) != 1 {
			t.Fatal("Auditors read Exceptions")
		}
		testutil.RequireStatus(t, w.exceptions(w.owner), http.StatusForbidden, "")
		w.due()
		tl.drain(t)
		requireGrant(t, w.teamGrant(w.jiraViewer), "SUSPENDED", "SUSPENDED / VERIFIED")
		if len(w.gaps(w.is1)) != 0 {
			t.Fatal("a verified suspension is no longer a gap")
		}
	})
	t.Run("API refused by the tool, then retried", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
		tl.faults.RespondNext(http.StatusConflict, `{"error":"not_granted"}`)
		tl.drain(t)
		op := w.failedOp(w.admin)
		if op["type"] != "SUSPEND" || len(w.gaps(w.is1)) != 1 {
			t.Fatalf("failed %v, gaps %v", op, w.gaps(w.is1))
		}
		testutil.RequireStatus(t, retry(w.admin, op), http.StatusOK, "")
		if g := w.teamGrant(w.jiraViewer); g["status"] != "ACTIVE" {
			t.Fatalf("a retried SUSPEND moved the grant: %v", g)
		}
		tl.drain(t)
		requireGrant(t, w.teamGrant(w.jiraViewer), "SUSPENDED", "SUSPENDED / VERIFIED")
		if len(w.gaps(w.is1)) != 0 {
			t.Fatal("gap after the retry was verified")
		}
	})
	t.Run("API failed, then the access is revoked", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
		tl.faults.RespondNext(http.StatusConflict, `{"error":"not_granted"}`)
		tl.drain(t)
		w.mustRevoke(w.mgr.c, w.teamGrant(w.jiraViewer))
		if len(w.gaps(w.is1)) != 0 || w.count("SELECT count(*) FROM operations WHERE type = 'SUSPEND' AND status = 'CANCELLED'") != 1 {
			t.Fatal("the removal did not take over the failed suspension")
		}
		tl.drain(t)
		requireGrant(t, w.teamGrant(w.jiraViewer), "REVOKED", "REVOKED / VERIFIED")
	})
	t.Run("Manual task reported failed", func(t *testing.T) {
		w := newWorld(t)
		f := w.fulfillers()
		w.fulfilled(f, f.manual)
		w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
		task := w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "SUSPEND"), "claim", nil)
		w.mustAct(f.fixer, task, "fail", map[string]any{"reason": "the admin console is down"})
		if g := w.teamGrant(f.manual); g["status"] != "ACTIVE" || g["suspension"] != "SUSPENSION_FAILED" || len(w.gaps(w.is1)) != 1 {
			t.Fatalf("grant %v, gaps %v", g, w.gaps(w.is1))
		}
		testutil.RequireStatus(t, retry(w.admin, w.failedOp(w.admin)), http.StatusOK, "")
		task = w.mustAct(f.fixer2, w.mustTask(f.fixer2, f.manual, "SUSPEND"), "claim", nil)
		w.mustAct(f.fixer2, task, "confirm", nil)
		requireGrant(t, w.teamGrant(f.manual), "SUSPENDED", "SUSPENDED / SELF_REPORTED")
		if len(w.gaps(w.is1)) != 0 {
			t.Fatal("gap after the confirmed retry")
		}
	})
}

// A grant that activated after the suspension and whose SUSPEND then fails is a gap too.
func TestSuspensionGapForAGrantActivatedAfterSuspension(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	done := w.inFlight(tl)
	w.mustLifecycle(w.hr(), w.dev.ID, "suspend")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	tl.faults.RespondNext(http.StatusConflict, `{"error":"not_granted"}`)
	tl.drain(t)
	if gaps := w.gaps(w.is1); len(gaps) != 1 || gaps[0].(map[string]any)["grantId"] != w.teamGrant(w.jiraViewer)["id"] {
		t.Fatalf("gaps = %v", gaps)
	}
}

func TestSuspendedClaimerTaskCanBeTakenOver(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	w.approved(f.manual)
	task := w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "GRANT"), "claim", nil)
	testutil.RequireStatus(t, act(f.fixer2, task, "claim", nil), http.StatusConflict, "task_claimed")
	w.mustLifecycle(w.hr(), f.fixer.ID, "suspend")
	w.mustAct(f.fixer2, task, "claim", nil)
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'TASK_CLAIMED' AND actor_id = $1 AND details->>'takeover' = 'true'`,
		f.fixer2.ID); n != 1 {
		t.Fatal("not a takeover")
	}
}

// inFlightThenFail is inFlight whose tool call applies and whose status lookup then fails:
// the tool changed, the attempt failed.
func (w *world) inFlightThenFail(tl *tools) <-chan error {
	w.t.Helper()
	tl.faults.DelayNext(500 * time.Millisecond) // the call
	tl.faults.FailNext(1, http.StatusServiceUnavailable)
	done := make(chan error, 1)
	go func() { _, err := tl.runner.RunOnce(context.Background()); done <- err }()
	for deadline := time.Now().Add(5 * time.Second); w.count("SELECT count(*) FROM operations WHERE status = 'IN_PROGRESS'") == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			w.t.Fatal("never claimed")
		}
	}
	return done
}

// suspended gives dev an ACTIVE Jira Viewer, suspended and verified.
func (w *world) suspended(tl *tools, hr person) map[string]any {
	w.t.Helper()
	w.approved(w.jiraViewer)
	tl.drain(w.t)
	w.mustLifecycle(hr, w.dev.ID, "suspend")
	tl.drain(w.t)
	return w.teamGrant(w.jiraViewer)
}

func TestReactivateAPI(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	hr := w.hr()
	g := w.suspended(tl, hr)
	before := w.version(w.dev.ID)
	e := w.mustLifecycle(hr, w.dev.ID, "reactivate")
	if e["status"] != "ACTIVE" || int(e["version"].(float64)) != before+1 {
		t.Fatalf("employee = %v", e)
	}
	testutil.RequireStatus(t, w.get(w.dev.c, "/api/v1/access"), http.StatusUnauthorized, "") // the old session stays ended
	if g := w.teamGrant(w.jiraViewer); g["status"] != "SUSPENDED" || g["suspension"] != "RESUME_REQUESTED" ||
		w.count("SELECT count(*) FROM operations WHERE grant_id = $1 AND type = 'RESUME' AND priority = 2", g["id"]) != 1 {
		t.Fatalf("grant before the runner = %v", g)
	}
	tl.drain(t)
	g = w.teamGrant(w.jiraViewer)
	requireGrant(t, g, "ACTIVE", "ACTIVE / VERIFIED")
	if w.openIntervals(g) != 0 || tl.state(t, g, w.dev.Email) != "ACTIVE" || w.audits("EMPLOYEE_REACTIVATED") != 1 || w.audits("GRANT_RESUMED") != 1 {
		t.Fatal("resume not recorded")
	}
	dev := testutil.NewClient(t, w.srv)
	testutil.RequireStatus(t, dev.Login(w.dev.Email, testutil.Password), http.StatusOK, "")
}

func TestReactivateReevaluates(t *testing.T) {
	t.Run("retired level", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		hr := w.hr()
		g := w.suspended(tl, hr)
		w.exec("UPDATE access_levels SET status = 'RETIRED' WHERE id = $1", w.jiraViewer)
		w.mustLifecycle(hr, w.dev.ID, "reactivate")
		g = w.teamGrant(w.jiraViewer)
		if g["status"] != "REVOCATION_REQUESTED" || g["closedReason"] != "access level retired" || w.ops(g, "RESUME", "PENDING") != 0 {
			t.Fatalf("grant = %v", g)
		}
	})
	t.Run("expired", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		hr := w.hr()
		g := w.suspended(tl, hr)
		w.expiresOn(g, in(-1)) // the sweep has not run
		w.mustLifecycle(hr, w.dev.ID, "reactivate")
		g = w.teamGrant(w.jiraViewer)
		if g["status"] != "REVOCATION_REQUESTED" || g["closedReason"] != "expired" || w.ops(g, "RESUME", "PENDING") != 0 {
			t.Fatalf("grant = %v", g)
		}
	})
	t.Run("failed suspension, then retired", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		hr := w.hr()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(hr, w.dev.ID, "suspend")
		tl.faults.RespondNext(http.StatusConflict, `{"error":"level_suspended"}`) // the tool may have applied it
		tl.drain(t)
		w.exec("UPDATE access_levels SET status = 'RETIRED' WHERE id = $1", w.jiraViewer)
		w.mustLifecycle(hr, w.dev.ID, "reactivate")
		g := w.teamGrant(w.jiraViewer)
		if g["status"] != "REVOCATION_REQUESTED" || w.ops(g, "RESUME", "PENDING") != 0 {
			t.Fatalf("a retired level would be resumed: %v", g)
		}
	})
	t.Run("revoked and cancelled stay so", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		hr := w.hr()
		w.mustSubmit(w.dev.c, body(nil, item(w.github)))
		g := w.suspended(tl, hr)
		w.mustRevoke(w.mgr.c, g)
		tl.drain(t)
		w.mustLifecycle(hr, w.dev.ID, "reactivate")
		tl.drain(t)
		requireGrant(t, w.teamGrant(w.jiraViewer), "REVOKED", "REVOKED / VERIFIED")
		if w.count("SELECT count(*) FROM operations WHERE type = 'RESUME'") != 0 ||
			w.count("SELECT count(*) FROM request_items WHERE beneficiary_id = $1 AND status = 'PENDING_APPROVAL'", w.dev.ID) != 0 {
			t.Fatal("something was restored")
		}
	})
}

func TestReactivateDuringSuspend(t *testing.T) {
	t.Run("never sent", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		hr := w.hr()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(hr, w.dev.ID, "suspend")
		w.mustLifecycle(hr, w.dev.ID, "reactivate")
		g := w.teamGrant(w.jiraViewer)
		if g["status"] != "ACTIVE" || g["suspension"] != nil || w.ops(g, "SUSPEND", "CANCELLED") != 1 ||
			w.count("SELECT count(*) FROM operations WHERE type = 'RESUME'") != 0 {
			t.Fatalf("grant = %v", g)
		}
	})
	t.Run("failed: resumed", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		hr := w.hr()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(hr, w.dev.ID, "suspend")
		tl.faults.RespondNext(http.StatusConflict, `{"error":"level_suspended"}`)
		tl.drain(t)
		w.mustLifecycle(hr, w.dev.ID, "reactivate")
		g := w.teamGrant(w.jiraViewer)
		if w.ops(g, "SUSPEND", "CANCELLED") != 1 || w.ops(g, "RESUME", "PENDING") != 1 || len(w.failedOps(w.admin)) != 0 {
			t.Fatalf("grant %v; the stale SUSPEND must leave Exceptions", g)
		}
		tl.drain(t)
		g = w.teamGrant(w.jiraViewer)
		requireGrant(t, g, "ACTIVE", "ACTIVE / VERIFIED")
		if tl.state(t, g, w.dev.Email) != "ACTIVE" {
			t.Fatal("tool state")
		}
	})
	t.Run("in flight: resumed after it", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		hr := w.hr()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(hr, w.dev.ID, "suspend")
		done := w.inFlight(tl)
		w.mustLifecycle(hr, w.dev.ID, "reactivate")
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		tl.drain(t)
		g := w.teamGrant(w.jiraViewer)
		requireGrant(t, g, "ACTIVE", "ACTIVE / VERIFIED")
		if tl.state(t, g, w.dev.Email) != "ACTIVE" || w.openIntervals(g) != 0 ||
			w.count("SELECT count(*) FROM grant_suspensions WHERE grant_id = $1", g["id"]) != 1 {
			t.Fatal("the suspension in the tool was not recorded and ended")
		}
	})
}

func (w *world) failedOps(p person) []any {
	w.t.Helper()
	r := w.exceptions(p)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	return r.Body["failedOperations"].([]any)
}

// A SUSPEND or RESUME overtaken by the opposite action or a removal is cancelled, never
// retried or failed, so it cannot apply the wrong state later (review findings 1-3).
func TestStaleSuspensionOperationsAreCancelled(t *testing.T) {
	t.Run("SUSPEND failing after reactivation", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		hr := w.hr()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(hr, w.dev.ID, "suspend")
		done := w.inFlightThenFail(tl)
		w.mustLifecycle(hr, w.dev.ID, "reactivate")
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		g := w.teamGrant(w.jiraViewer)
		if w.ops(g, "SUSPEND", "CANCELLED") != 1 || w.ops(g, "SUSPEND", "PENDING")+w.ops(g, "SUSPEND", "FAILED") != 0 {
			t.Fatal("the stale SUSPEND would be retried")
		}
		tl.drain(t)
		requireGrant(t, w.teamGrant(w.jiraViewer), "ACTIVE", "ACTIVE / VERIFIED")
		if tl.state(t, g, w.dev.Email) != "ACTIVE" {
			t.Fatal("the reactivated employee's access is disabled in the tool")
		}
	})
	t.Run("RESUME failing after a new suspension", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		hr := w.hr()
		g := w.suspended(tl, hr)
		w.mustLifecycle(hr, w.dev.ID, "reactivate")
		done := w.inFlightThenFail(tl)
		w.mustLifecycle(hr, w.dev.ID, "suspend")
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if w.ops(g, "RESUME", "CANCELLED") != 1 || w.ops(g, "RESUME", "PENDING")+w.ops(g, "RESUME", "FAILED") != 0 {
			t.Fatal("the stale RESUME would be retried")
		}
		tl.drain(t)
		requireGrant(t, w.teamGrant(w.jiraViewer), "SUSPENDED", "SUSPENDED / VERIFIED")
		if tl.state(t, g, w.dev.Email) != "SUSPENDED" {
			t.Fatal("the suspended employee's access is active in the tool")
		}
	})
	t.Run("RESUME verified after a revoke", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		hr := w.hr()
		g := w.suspended(tl, hr)
		w.mustLifecycle(hr, w.dev.ID, "reactivate")
		done := w.inFlight(tl)
		w.mustRevoke(w.mgr.c, w.teamGrant(w.jiraViewer))
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if w.openIntervals(g) != 0 || w.count(`SELECT count(*) FROM grant_suspensions s JOIN operations o ON o.grant_id = s.grant_id
			AND o.type = 'RESUME' WHERE s.grant_id = $1 AND s.effective_to = o.completed_at`, g["id"]) != 1 {
			t.Fatal("the interval must end when the tool resumed the level")
		}
		tl.drain(t)
		requireGrant(t, w.teamGrant(w.jiraViewer), "REVOKED", "REVOKED / VERIFIED")
	})
	t.Run("retry of a superseded operation", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		hr := w.hr()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(hr, w.dev.ID, "suspend")
		tl.faults.RespondNext(http.StatusConflict, `{"error":"not_granted"}`)
		tl.drain(t)
		op := w.failedOp(w.admin)
		w.mustLifecycle(hr, w.dev.ID, "reactivate")
		w.exec("UPDATE operations SET status = 'CANCELLED' WHERE type = 'RESUME'")
		w.exec("UPDATE operations SET status = 'FAILED' WHERE id = $1", op["id"]) // as if left behind
		op["version"] = w.count("SELECT version FROM operations WHERE id = $1", op["id"])
		testutil.RequireStatus(t, retry(w.admin, op), http.StatusConflict, "operation_superseded")
	})
}

// A transaction that waited for a lock inserts its operation with its start time, which can
// be earlier than an operation committed meanwhile; the order is the insert order (uuidv7
// ids), so a backdated created_at changes nothing (review 2026-10-29, finding 2).
func TestOperationOrderIsInsertOrder(t *testing.T) {
	backdate := func(w *world, typ string, before string) {
		w.exec(`UPDATE operations SET created_at = (SELECT created_at FROM operations WHERE type = $2) - interval '1 minute'
			WHERE type = $1`, typ, before)
	}
	t.Run("a removal waits for the GRANT", func(t *testing.T) {
		w := newWorld(t)
		f := w.fulfillers()
		w.approved(f.manual)
		w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "GRANT"), "claim", nil)
		w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, f.manual))
		backdate(w, "REVOKE", "GRANT")
		if w.task(f.fixer2, f.manual, "REVOKE") != nil {
			t.Fatal("the removal task is ready while the GRANT task is claimed")
		}
	})
	t.Run("a SUSPEND overtaken by a RESUME", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		hr := w.hr()
		w.approved(w.jiraViewer)
		tl.drain(t)
		w.mustLifecycle(hr, w.dev.ID, "suspend")
		done := w.inFlightThenFail(tl)
		w.mustLifecycle(hr, w.dev.ID, "reactivate")
		backdate(w, "RESUME", "SUSPEND")
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		g := w.teamGrant(w.jiraViewer)
		if w.ops(g, "SUSPEND", "CANCELLED") != 1 || g["suspension"] != "RESUME_REQUESTED" {
			t.Fatalf("grant %v; the stale SUSPEND must be cancelled", g)
		}
	})
}

func TestReactivateResumeManual(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	hr := w.hr()
	w.fulfilled(f, f.manual)
	w.mustLifecycle(hr, w.dev.ID, "suspend")
	w.mustAct(f.fixer, w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "SUSPEND"), "claim", nil), "confirm", nil)
	w.mustLifecycle(hr, w.dev.ID, "reactivate")
	w.mustAct(f.fixer, w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "RESUME"), "claim", nil), "confirm", nil)
	requireGrant(t, w.teamGrant(f.manual), "ACTIVE", "ACTIVE / SELF_REPORTED")
}

func TestReactivateRules(t *testing.T) {
	w := newWorld(t)
	hr := w.hr()
	testutil.RequireStatus(t, w.lifecycle(hr, w.dev.ID, "reactivate"), http.StatusConflict, "employee_status")
	w.mustLifecycle(w.admin, w.is1.ID, "suspend")
	testutil.RequireStatus(t, w.lifecycle(hr, w.is1.ID, "reactivate"), http.StatusForbidden, "privileged_reset_requires_super_admin")
	testutil.RequireStatus(t, w.lifecycle(w.owner, w.is1.ID, "reactivate"), http.StatusForbidden, "")
	w.mustLifecycle(w.admin, w.is1.ID, "reactivate")
	w.exec("UPDATE employees SET status = 'TERMINATED' WHERE id = $1", w.dev.ID)
	testutil.RequireStatus(t, w.lifecycle(hr, w.dev.ID, "reactivate"), http.StatusConflict, "employee_status")
}

func (w *world) securityRevocations(p person) []any {
	w.t.Helper()
	r := w.exceptions(p)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	return r.Body["securityRevocations"].([]any)
}

// The "14 leavers" path: every open grant, whatever its state, gets a priority-0 removal
// that stays in Exceptions until verified or confirmed (D7, O7).
func TestTerminateRemovesEverything(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	f := w.fulfillers()
	hr := w.hr()
	w.approved(w.jiraViewer)
	tl.drain(t)
	w.fulfilled(f, f.manual)
	w.approved(f.vendor)
	w.mustAct(f.fixer, w.mustAct(f.fixer, w.mustTask(f.fixer, f.vendor, "GRANT"), "claim", nil), "fail", map[string]any{"reason": "no seats"})
	w.mustLifecycle(hr, w.dev.ID, "suspend") // API: SUSPEND; Manual: a SUSPEND task; Vendor: FAILED stays
	tl.drain(t)
	w.exec("INSERT INTO role_assignments (employee_id, role, valid_from) VALUES ($1, 'AUDITOR', now())", w.dev.ID)
	w.member(w.dev, w.jira)

	e := w.mustLifecycle(hr, w.dev.ID, "terminate")
	if e["status"] != "TERMINATED" || len(e["roles"].([]any)) != 0 || len(e["adminGroups"].([]any)) != 0 {
		t.Fatalf("employee = %v", e)
	}
	if w.audits("ROLE_REMOVED") != 1 || w.audits("ADMIN_GROUP_MEMBER_REMOVED") != 1 || w.audits("EMPLOYEE_TERMINATED") != 1 {
		t.Fatal("roles, memberships or the termination not audited")
	}
	for _, level := range []uuid.UUID{w.jiraViewer, f.manual, f.vendor} {
		g := w.teamGrant(level)
		if g["status"] != "REVOCATION_REQUESTED" || w.count(`SELECT count(*) FROM operations WHERE grant_id = $1 AND type = 'REVOKE'
			AND priority = 0 AND reason = 'TERMINATION' AND status = 'PENDING'`, g["id"]) != 1 {
			t.Fatalf("%v: %v", level, g)
		}
		if w.count("SELECT count(*) FROM operations WHERE grant_id = $1 AND type IN ('SUSPEND', 'RESUME') AND status IN ('PENDING', 'FAILED')", g["id"]) != 0 {
			t.Fatalf("%v: a SUSPEND or RESUME is still waiting", level)
		}
	}
	if n := len(w.securityRevocations(w.is1)); n != 3 {
		t.Fatalf("security revocations = %d", n)
	}
	tl.drain(t)
	w.mustAct(f.fixer, w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "REVOKE"), "claim", nil), "confirm", nil)
	task := w.mustAct(f.fixer, w.mustTask(f.fixer, f.vendor, "REVOKE"), "claim", nil)
	w.mustAct(f.fixer, w.mustAct(f.fixer, task, "ticket", map[string]any{"ticketRef": "VND-3"}), "confirm", nil)
	if n := len(w.securityRevocations(w.is1)); n != 0 {
		t.Fatalf("security revocations after removal = %d", n)
	}
	g := w.teamGrant(w.jiraViewer)
	requireGrant(t, g, "REVOKED", "REVOKED / VERIFIED")
	if tl.state(t, g, w.dev.Email) != "" || w.openIntervals(g) != 0 {
		t.Fatal("the tool still holds the level")
	}
}

// A grant never sent to the tool has nothing to remove: termination cancels it with no call.
func TestTerminateCancelsNeverSentGrant(t *testing.T) {
	w := newWorld(t)
	w.approved(w.jiraViewer) // queued, not yet run
	w.mustLifecycle(w.hr(), w.dev.ID, "terminate")
	g := w.teamGrant(w.jiraViewer)
	if g["status"] != "CANCELLED" || w.ops(g, "GRANT", "CANCELLED") != 1 ||
		w.count("SELECT count(*) FROM operations WHERE grant_id = $1 AND type = 'REVOKE'", g["id"]) != 0 {
		t.Fatalf("grant = %v", g)
	}
	if n := len(w.securityRevocations(w.is1)); n != 0 {
		t.Fatalf("security revocations = %d", n)
	}
}

// An API GRANT call in flight is not cancelled: the removal waits for it, so the GRANT
// cannot re-apply access after the removal (D5, D7).
func TestTerminateDuringGrantInFlight(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	done := w.inFlight(tl)
	w.mustLifecycle(w.hr(), w.dev.ID, "terminate")
	g := w.teamGrant(w.jiraViewer)
	if w.ops(g, "GRANT", "IN_PROGRESS") != 1 {
		t.Fatal("the GRANT call in flight was cancelled")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	tl.drain(t)
	g = w.teamGrant(w.jiraViewer)
	requireGrant(t, g, "REVOKED", "REVOKED / VERIFIED")
	if tl.state(t, g, w.dev.Email) != "" {
		t.Fatal("the leaver still holds the level")
	}
}

func TestTerminateDuringSuspendInFlight(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	hr := w.hr()
	w.approved(w.jiraViewer)
	tl.drain(t)
	w.mustLifecycle(hr, w.dev.ID, "suspend")
	done := w.inFlightThenFail(tl)
	w.mustLifecycle(hr, w.dev.ID, "terminate")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	g := w.teamGrant(w.jiraViewer)
	if w.ops(g, "SUSPEND", "CANCELLED") != 1 {
		t.Fatal("the failing SUSPEND would hold up the removal")
	}
	if found, err := tl.runner.RunOnce(context.Background()); err != nil || !found || w.ops(g, "REVOKE", "SUCCEEDED") != 1 {
		t.Fatalf("the REVOKE did not run next (found %v, %v)", found, err)
	}
}

func TestTerminateDuringResumeInFlight(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	hr := w.hr()
	g := w.suspended(tl, hr)
	w.mustLifecycle(hr, w.dev.ID, "reactivate")
	done := w.inFlight(tl)
	w.mustLifecycle(hr, w.dev.ID, "terminate")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if w.openIntervals(g) != 0 {
		t.Fatal("the interval must end when the tool resumed the level")
	}
	tl.drain(t)
	g = w.teamGrant(w.jiraViewer)
	requireGrant(t, g, "REVOKED", "REVOKED / VERIFIED")
	if tl.state(t, g, w.dev.Email) != "" {
		t.Fatal("level still held")
	}
}

func TestTerminationRemovalsRunFirst(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	hr := w.hr()
	w.approved(w.jiraViewer)
	w.approveAll(items(w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))))[0])
	w.approve(w.skip.c, open(t, items(w.mustSubmit(w.mgr.c, body(nil, item(w.jiraViewer))))[0]))
	tl.drain(t)
	w.mustRevoke(w.mgr.c, w.myGrant(w.mgr.c, w.jiraViewer))                                 // an older ordinary removal (priority 1)
	w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.awsAdmin))                                   // the employee's own, already open
	w.approve(w.mgr.c, open(t, items(w.mustSubmit(w.dev.c, body(nil, item(w.github))))[0])) // a priority-2 grant
	w.mustLifecycle(hr, w.dev.ID, "terminate")
	for range 2 {
		if _, err := tl.runner.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := w.count(`SELECT count(*) FROM operations o JOIN grants g ON g.id = o.grant_id
		WHERE g.employee_id = $1 AND o.type = 'REVOKE' AND o.status = 'SUCCEEDED' AND o.priority = 0`, w.dev.ID); n != 2 {
		t.Fatalf("the leaver's removals did not run first: %d", n)
	}
	if w.count(`SELECT count(*) FROM operations o JOIN grants g ON g.id = o.grant_id WHERE g.employee_id = $1 AND o.status = 'PENDING'`, w.mgr.ID) != 1 {
		t.Fatal("the older ordinary removal ran first")
	}
}

func TestTerminateReplacementInFlight(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	old, _ := w.levelChange(tl)
	done := w.inFlight(tl)
	w.mustLifecycle(w.hr(), w.dev.ID, "terminate")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	tl.drain(t)
	niu := w.teamGrant(w.jiraAdmin)
	requireGrant(t, niu, "REVOKED", "REVOKED / VERIFIED")
	requireGrant(t, w.teamGrant(w.jiraViewer), "REVOKED", "REVOKED / VERIFIED")
	if w.removals(old) != 1 || tl.held(t, niu, w.dev.Email) != 0 || tl.held(t, old, w.dev.Email) != 0 ||
		w.count("SELECT count(*) FROM audit_events WHERE action = 'GRANT_STATUS' AND entity_id = $1", niu["id"]) != 0 {
		t.Fatal("access left behind")
	}
	if w.count("SELECT count(*) FROM grants WHERE id = $1 AND status = 'ACTIVE'", niu["id"]) != 0 {
		t.Fatal("the replacement became ACTIVE")
	}
}

// Two Super Admins terminating each other: one wins, the other is signed out by it.
func TestSuperAdminsTerminatingEachOther(t *testing.T) {
	w := newWorld(t)
	other := w.person(testutil.EmployeeSpec{Roles: []string{"SUPER_ADMIN"}})
	var wg sync.WaitGroup
	got := make([]testutil.Response, 2)
	wg.Go(func() { got[0] = w.lifecycle(w.admin, other.ID, "terminate") })
	wg.Go(func() { got[1] = w.lifecycle(other, w.admin.ID, "terminate") })
	wg.Wait()
	ok := 0
	for _, r := range got {
		switch r.Status {
		case http.StatusOK:
			ok++
		case http.StatusUnauthorized:
		default:
			t.Fatalf("response %d %v", r.Status, r.Body)
		}
	}
	if ok != 1 || w.count(`SELECT count(*) FROM employees e JOIN role_assignments r ON r.employee_id = e.id
		WHERE r.role = 'SUPER_ADMIN' AND r.valid_to IS NULL AND e.status = 'ACTIVE'`) != 1 {
		t.Fatalf("successes %d", ok)
	}
}

func TestTerminateRules(t *testing.T) {
	w := newWorld(t)
	hr := w.hr()
	testutil.RequireStatus(t, w.lifecycle(hr, w.is1.ID, "terminate"), http.StatusForbidden, "privileged_reset_requires_super_admin")
	testutil.RequireStatus(t, w.lifecycle(hr, hr.ID, "terminate"), http.StatusForbidden, "self_change_forbidden")
	testutil.RequireStatus(t, w.lifecycle(w.owner, w.dev.ID, "terminate"), http.StatusForbidden, "")
	w.mustLifecycle(hr, w.dev.ID, "suspend")
	w.mustLifecycle(hr, w.dev.ID, "terminate") // a suspended employee can leave
	for _, action := range []string{"terminate", "suspend", "reactivate"} {
		testutil.RequireStatus(t, w.lifecycle(hr, w.dev.ID, action), http.StatusConflict, "employee_status")
	}

	// A concurrent termination and suspension: one outcome, one set of operations.
	w2 := newWorld(t)
	tl := w2.startTools()
	w2.approved(w2.jiraViewer)
	tl.drain(t)
	hr2, hr3 := w2.hr(), w2.hr()
	v := w2.version(w2.dev.ID)
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, action := range []string{"terminate", "suspend"} {
		p := []person{hr2, hr3}[i]
		wg.Go(func() {
			statuses[i] = p.c.Do(http.MethodPost, "/api/v1/employees/"+w2.dev.ID.String()+"/"+action, map[string]any{"reason": "x", "version": v}).Status
		})
	}
	wg.Wait()
	if statuses[0]+statuses[1] != 200+409 {
		t.Fatalf("statuses = %v", statuses)
	}
	g := w2.teamGrant(w2.jiraViewer)
	if w2.count("SELECT count(*) FROM operations WHERE grant_id = $1 AND type IN ('SUSPEND', 'REVOKE')", g["id"]) != 1 {
		t.Fatal("both actions applied")
	}
}

func TestTerminateCancelsPendingWork(t *testing.T) {
	w := newWorld(t)
	w.mustSubmit(w.mgr.c, body(&w.dev.ID, item(w.github))) // on the employee's behalf
	w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer)))   // a step for the manager
	w.mustLifecycle(w.hr(), w.mgr.ID, "terminate")         // the manager leaves: their step is re-routed
	w.mustLifecycle(w.hr(), w.dev.ID, "terminate")
	if w.count("SELECT count(*) FROM request_items WHERE beneficiary_id = $1 AND status = 'PENDING_APPROVAL'", w.dev.ID) != 0 {
		t.Fatal("a pending item survived")
	}
	if w.audits("STEP_REROUTED") == 0 {
		t.Fatal("the manager's step was not re-routed")
	}
}

// A RESUME queued while the SUSPEND is in flight waits for it, so the tool applies them in
// order and the employee ends with the level active.
func TestResumeWaitsForSuspendInFlight(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	hr := w.hr()
	w.approved(w.jiraViewer)
	tl.drain(t)
	w.mustLifecycle(hr, w.dev.ID, "suspend")
	done := w.inFlight(tl)
	w.mustLifecycle(hr, w.dev.ID, "reactivate")
	if found, err := tl.runner.RunOnce(context.Background()); err != nil || found {
		t.Fatalf("the RESUME ran while the SUSPEND was in flight (found %v, %v)", found, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	tl.drain(t)
	g := w.teamGrant(w.jiraViewer)
	requireGrant(t, g, "ACTIVE", "ACTIVE / VERIFIED")
	if tl.state(t, g, w.dev.Email) != "ACTIVE" {
		t.Fatal("the tool holds the level suspended")
	}
}

// An HR Manager suspended while their own action waits for the lifecycle lock is refused:
// suspension blocks every action by the employee (D7).
func TestActorSuspendedWhileActing(t *testing.T) {
	w := newWorld(t)
	hr := w.hr()
	v := w.version(w.dev.ID)
	err := testutil.WhileLocked(t, w.db.App, func(ctx context.Context, tx pgx.Tx) error {
		if err := people.LockLifecycle(ctx, tx); err != nil { // as a suspension does
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE employees SET status = 'SUSPENDED' WHERE id = $1", hr.ID)
		return err
	}, func() error {
		r := hr.c.Do(http.MethodPost, "/api/v1/employees/"+w.dev.ID.String()+"/terminate", map[string]any{"reason": "x", "version": v})
		if r.Status != http.StatusUnauthorized {
			return fmt.Errorf("status %d %v; want 401", r.Status, r.Body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.count("SELECT count(*) FROM employees WHERE id = $1 AND status = 'ACTIVE'", w.dev.ID) != 1 {
		t.Fatal("the suspended actor's termination went through")
	}
}

// A task action or retry passed its session check, then waited for the grant's employee
// while its actor was suspended: it is refused, as an approval is (D7).
func TestTaskActorSuspendedWhileActing(t *testing.T) {
	for _, action := range []string{"confirm", "retry"} {
		t.Run(action, func(t *testing.T) {
			w := newWorld(t)
			f := w.fulfillers()
			w.approved(f.manual)
			task := w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "GRANT"), "claim", nil)
			call := func() testutil.Response { return act(f.fixer, task, "confirm", nil) }
			if action == "retry" {
				w.mustAct(f.fixer, task, "fail", map[string]any{"reason": "no seats"})
				op := w.failedOp(w.admin)
				call = func() testutil.Response { return retry(f.fixer, op) }
			}
			ctx := context.Background()
			tx, err := w.db.App.Begin(ctx) // holds dev: the action waits after its session check
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", w.dev.ID); err != nil {
				t.Fatal(err)
			}
			done := make(chan testutil.Response, 1)
			go func() { done <- call() }()
			w.waitForLock()
			w.mustLifecycle(w.hr(), f.fixer.ID, "suspend")
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			testutil.RequireStatus(t, <-done, http.StatusUnauthorized, "")
			if w.count("SELECT count(*) FROM operations WHERE id = $1 AND status IN ('SUCCEEDED', 'PENDING')", task["id"]) != 0 {
				t.Fatalf("the suspended admin's %s went through", action)
			}
		})
	}
}

// A claimed Manual SUSPEND or RESUME task is a person's to-do, not a call in flight:
// termination cancels it, so its claimer cannot re-enable a leaver and the removal task is
// ready at once (D7: termination overrides any pending resume).
func TestTerminateCancelsClaimedSuspensionTasks(t *testing.T) {
	for _, typ := range []string{"SUSPEND", "RESUME"} {
		t.Run(typ, func(t *testing.T) {
			w := newWorld(t)
			f := w.fulfillers()
			hr := w.hr()
			w.fulfilled(f, f.manual)
			w.mustLifecycle(hr, w.dev.ID, "suspend")
			if typ == "RESUME" {
				w.mustAct(f.fixer, w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "SUSPEND"), "claim", nil), "confirm", nil)
				w.mustLifecycle(hr, w.dev.ID, "reactivate")
			}
			claimed := w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, typ), "claim", nil)
			w.mustLifecycle(hr, w.dev.ID, "terminate")
			if r := act(f.fixer, claimed, "confirm", nil); r.Status != http.StatusConflict {
				t.Fatalf("confirm of the stale %s: %d %v; want 409", typ, r.Status, r.Body)
			}
			if w.count(`SELECT count(*) FROM audit_events a JOIN operations o ON o.id = a.entity_id
				WHERE a.action = 'OPERATION_CANCELLED' AND o.type = $1`, typ) != 1 {
				t.Fatal("the cancellation is not audited")
			}
			w.mustAct(f.fixer, w.mustAct(f.fixer, w.mustTask(f.fixer, f.manual, "REVOKE"), "claim", nil), "confirm", nil)
			requireGrant(t, w.teamGrant(f.manual), "REVOKED", "REVOKED / SELF_REPORTED")
			if w.audits("GRANT_RESUMED") != 0 || w.audits("GRANT_SUSPENDED") != map[string]int{"SUSPEND": 0, "RESUME": 1}[typ] {
				t.Fatal("the stale task changed the grant")
			}
		})
	}
}

// A claimed Manual or Vendor GRANT task is cancelled at termination, so the leaver's
// removal task is ready at once instead of waiting on its claimer (D7, A6), also when an
// ordinary revoke was already waiting for it. That revoke alone waits (TestRevokeWhileGrantTaskClaimed).
func TestTerminateCancelsClaimedGrantTask(t *testing.T) {
	for _, method := range []string{"MANUAL", "VENDOR"} {
		for _, revokedFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s revoked first %v", method, revokedFirst), func(t *testing.T) {
				w := newWorld(t)
				f := w.fulfillers()
				level := map[string]uuid.UUID{"MANUAL": f.manual, "VENDOR": f.vendor}[method]
				w.approved(level)
				claimed := w.mustAct(f.fixer, w.mustTask(f.fixer, level, "GRANT"), "claim", nil)
				if method == "VENDOR" {
					claimed = w.mustAct(f.fixer, claimed, "ticket", map[string]any{"ticketRef": "VND-7"})
				}
				if revokedFirst {
					w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, level))
				}
				w.mustLifecycle(w.hr(), w.dev.ID, "terminate")
				if r := act(f.fixer, claimed, "confirm", nil); r.Status != http.StatusConflict {
					t.Fatalf("confirm of the cancelled GRANT: %d %v; want 409", r.Status, r.Body)
				}
				g := w.teamGrant(level)
				if w.ops(g, "GRANT", "CANCELLED") != 1 || w.count(`SELECT count(*) FROM audit_events
					WHERE action = 'OPERATION_CANCELLED' AND entity_id = $1`, claimed["id"]) != 1 {
					t.Fatal("the claimed GRANT task was not cancelled and audited")
				}
				task := w.mustAct(f.fixer2, w.mustTask(f.fixer2, level, "REVOKE"), "claim", nil)
				if method == "VENDOR" {
					task = w.mustAct(f.fixer2, task, "ticket", map[string]any{"ticketRef": "VND-8"})
				}
				w.mustAct(f.fixer2, task, "confirm", nil)
				requireGrant(t, w.teamGrant(level), "REVOKED", "REVOKED / SELF_REPORTED")
			})
		}
	}
}

// A leaver's removal task heads the queue, ahead of older tasks (A6, D7).
func TestTerminationTasksComeFirst(t *testing.T) {
	w := newWorld(t)
	f := w.fulfillers()
	w.fulfilled(f, f.manual)
	w.approve(w.skip.c, open(t, items(w.mustSubmit(w.mgr.c, body(nil, item(f.vendor))))[0])) // an older GRANT task
	w.mustLifecycle(w.hr(), w.dev.ID, "terminate")
	tasks := w.taskList(f.fixer)
	if len(tasks) < 2 || tasks[0]["type"] != "REVOKE" {
		t.Fatalf("queue = %v", tasks)
	}
}
