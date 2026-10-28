package access_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/mocktools"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// rig is the runner against the real Mock Tool Service handler and database, behind Faults.
type rig struct {
	t      *testing.T
	db     testutil.DB
	mock   *pgxpool.Pool
	faults *testutil.Faults
	url    string // the mock behind Faults
	runner *access.Runner
}

// approvedGrant is a grant created by access.CreateGrant for a freshly approved item.
type approvedGrant struct {
	id, op, tool, level uuid.UUID
	emp                 testutil.Employee
}

func newRig(t *testing.T) *rig {
	t.Helper()
	mock := testutil.NewMockToolsDB(t, mocktools.Migrations)
	faults := testutil.NewFaults(mocktools.Handler(mock))
	srv := httptest.NewServer(faults)
	t.Cleanup(srv.Close)
	db := testutil.NewDB(t)
	return &rig{t: t, db: db, mock: mock, faults: faults, url: srv.URL, runner: access.NewRunner(db.App, access.NewHTTPToolClient(srv.URL, 5*time.Second))}
}

// grant approves an item for a new employee on a new tool of method and creates its grant.
func (r *rig) grant(method string) approvedGrant {
	r.t.Helper()
	ctx := context.Background()
	g := approvedGrant{emp: testutil.CreateEmployee(r.t, r.db, testutil.EmployeeSpec{})}
	g.tool = testutil.CreateTool(r.t, r.db, testutil.ToolSpec{Method: method})
	g.level = testutil.CreateLevel(r.t, r.db, g.tool, "User", false, false)
	err := platform.InTx(ctx, r.db.App, func(tx pgx.Tx, now time.Time) error {
		var item uuid.UUID
		if err := tx.QueryRow(ctx, `WITH req AS (INSERT INTO requests (submission_id, beneficiary_id, submitted_by)
			VALUES (uuidv7(), $1, $1) RETURNING id)
			INSERT INTO request_items (request_id, beneficiary_id, tool_id, access_level_id, justification, duration_type, status)
			SELECT id, $1, $2, $3, 'x', 'UNTIL_REVOKED', 'APPROVED' FROM req RETURNING id`, g.emp.ID, g.tool, g.level).Scan(&item); err != nil {
			return err
		}
		var err error
		g.id, err = access.CreateGrant(ctx, tx, now, g.emp.ID, item)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	r.scan("SELECT id FROM operations WHERE grant_id = $1", []any{g.id}, &g.op)
	return g
}

func (r *rig) scan(sql string, args []any, dst ...any) {
	r.t.Helper()
	if err := r.db.App.QueryRow(context.Background(), sql, args...).Scan(dst...); err != nil {
		r.t.Fatalf("%s: %v", sql, err)
	}
}

func (r *rig) count(sql string, args ...any) int {
	r.t.Helper()
	var n int
	r.scan(sql, args, &n)
	return n
}

func (r *rig) exec(sql string, args ...any) {
	r.t.Helper()
	if _, err := r.db.App.Exec(context.Background(), sql, args...); err != nil {
		r.t.Fatalf("%s: %v", sql, err)
	}
}

// runOnce runs one step and requires that it found an operation (or not).
func (r *rig) runOnce(wantFound bool) {
	r.t.Helper()
	found, err := r.runner.RunOnce(context.Background())
	if err != nil || found != wantFound {
		r.t.Fatalf("RunOnce = %v, %v; want %v", found, err, wantFound)
	}
}

// held is how many permissions the mock holds for the grant's (tool, account, level).
func (r *rig) held(g approvedGrant) int {
	r.t.Helper()
	var n int
	if err := r.mock.QueryRow(context.Background(), "SELECT count(*) FROM permissions WHERE tool = $1 AND account = $2 AND level = $3",
		g.tool.String(), g.emp.Email, g.level.String()).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

// state is the grant's status and its operation's status and attempts.
func (r *rig) state(g approvedGrant) (grant, op string, attempts int) {
	r.t.Helper()
	r.scan("SELECT g.status, o.status, o.attempts FROM grants g JOIN operations o ON o.id = $2 WHERE g.id = $1",
		[]any{g.id, g.op}, &grant, &op, &attempts)
	return
}

func (r *rig) requireState(g approvedGrant, grant, op string, attempts int) {
	r.t.Helper()
	if gs, os, n := r.state(g); gs != grant || os != op || n != attempts {
		r.t.Fatalf("grant %s, operation %s after %d attempts; want %s, %s, %d", gs, os, n, grant, op, attempts)
	}
}

func (r *rig) audits(action string) int {
	return r.count("SELECT count(*) FROM audit_events WHERE action = $1", action)
}

// due makes the operation's next attempt due now (D12: no waiting in tests).
func (r *rig) due(op uuid.UUID) {
	r.exec("UPDATE operations SET next_attempt_at = now() WHERE id = $1", op)
}

func TestRunnerProvisionsAndVerifies(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	r.runOnce(true)
	r.requireState(g, "ACTIVE", "SUCCEEDED", 1)
	if n := r.count(`SELECT count(*) FROM grants g JOIN audit_events a ON a.entity_id = g.id AND a.action = 'GRANT_ACTIVATED'
		WHERE g.id = $1 AND g.activation_evidence = 'VERIFIED' AND g.effective_from = a.occurred_at AND a.actor_id IS NULL`, g.id); n != 1 {
		t.Fatalf("verified activation recorded at its own time by the system = %d", n)
	}
	if r.held(g) != 1 {
		t.Fatal("the mock does not hold the permission")
	}
	r.runOnce(false) // nothing left
}

func TestRunnerClaimsOnlyAPIOperations(t *testing.T) {
	r := newRig(t)
	for _, method := range []string{"MANUAL", "VENDOR"} {
		g := r.grant(method)
		r.runOnce(false)
		r.requireState(g, "PENDING_FULFILLMENT", "PENDING", 0)
	}
	if r.faults.Requests() != 0 {
		t.Fatal("a Manual or Vendor grant reached the Mock Tool Service")
	}
}

// Regression (PR #4 review): a level or tool retired between approval and the claim is
// never provisioned.
func TestRunnerSkipsRetiredLevel(t *testing.T) {
	for _, table := range []string{"access_levels", "tools"} {
		r := newRig(t)
		g := r.grant("API")
		r.exec("UPDATE "+table+" SET status = 'RETIRED' WHERE id IN ($1, $2)", g.tool, g.level)
		r.runOnce(true)
		r.requireState(g, "CANCELLED", "CANCELLED", 0)
		reason := map[string]string{"access_levels": "access level retired", "tools": "tool retired"}[table]
		if n := r.count("SELECT count(*) FROM audit_events WHERE action = 'GRANT_CANCELLED' AND reason = $1", reason); n != 1 ||
			r.faults.Requests() != 0 || r.audits("GRANT_ACTIVATED") != 0 {
			t.Fatalf("%s: requests %d, GRANT_CANCELLED %d", table, r.faults.Requests(), n)
		}
	}
}

// Once an attempt may have reached the tool, retirement no longer cancels: the grant is
// completed and verified, so no permission is left behind unrecorded.
func TestRetiredAfterAnAttemptIsStillCompleted(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	r.faults.ApplyThenFail() // the tool applied it; the answer was lost
	r.runOnce(true)
	r.requireState(g, "PROVISIONING", "PENDING", 1)
	r.exec("UPDATE access_levels SET status = 'RETIRED' WHERE id = $1", g.level)
	r.due(g.op)
	r.runOnce(true)
	r.requireState(g, "ACTIVE", "SUCCEEDED", 2)
	if r.held(g) != 1 {
		t.Fatal("permission not held")
	}
}

func TestSuccessWithWrongStateIsNotSuccess(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	// The tool says APPLIED, but the status lookup shows no permission.
	r.faults.RespondNext(http.StatusOK, `{"operationId":"`+g.op.String()+`","type":"GRANT","tool":"`+g.tool.String()+
		`","account":"`+g.emp.Email+`","level":"`+g.level.String()+`","result":"APPLIED"}`)
	r.runOnce(true)
	r.requireState(g, "PROVISIONING", "PENDING", 1)
	if n := r.count(`SELECT count(*) FROM operations o JOIN audit_events a ON a.entity_id = o.id AND a.action = 'OPERATION_ATTEMPT_FAILED'
		WHERE o.id = $1 AND o.next_attempt_at = a.occurred_at + interval '30 seconds' AND o.last_error <> ''
		  AND o.lease_token IS NULL AND o.lease_until IS NULL`, g.op); n != 1 {
		t.Fatalf("failed attempt with a 30 s backoff = %d", n)
	}
	r.runOnce(false) // not due yet
}

func TestBackoffThenFailed(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	r.faults.FailNext(5, http.StatusServiceUnavailable)
	waits := []string{"30 seconds", "1 minute", "2 minutes", "4 minutes"}
	for i := range 5 {
		r.runOnce(true)
		if i < 4 {
			if n := r.count(`SELECT count(*) FROM operations WHERE id = $1 AND next_attempt_at =
				(SELECT max(occurred_at) FROM audit_events WHERE action = 'OPERATION_ATTEMPT_FAILED') + $2::interval`, g.op, waits[i]); n != 1 {
				t.Fatalf("after attempt %d: want a wait of %s", i+1, waits[i])
			}
			r.due(g.op)
		}
	}
	r.requireState(g, "FAILED", "FAILED", 5)
	if a, f := r.audits("OPERATION_ATTEMPT_FAILED"), r.audits("OPERATION_FAILED"); a != 5 || f != 1 {
		t.Fatalf("OPERATION_ATTEMPT_FAILED %d, OPERATION_FAILED %d", a, f)
	}
	r.due(g.op)
	r.runOnce(false) // a failed operation waits for a manual retry
}

func Test409IsFailedAtOnce(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	r.faults.RespondNext(http.StatusConflict, `{"error":"level_suspended"}`)
	r.runOnce(true)
	r.requireState(g, "FAILED", "FAILED", 1)
	if r.audits("OPERATION_FAILED") != 1 {
		t.Fatal("no OPERATION_FAILED")
	}
}

func TestTimeoutIsAFailedAttempt(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	r.runner = access.NewRunner(r.db.App, access.NewHTTPToolClient(r.url, timeout))
	r.faults.DelayNext(2 * timeout)
	r.runOnce(true)
	r.requireState(g, "PROVISIONING", "PENDING", 1)
}

// A worker that stopped mid-call loses its lease; the operation is claimed again.
func TestLeaseExpiryReclaims(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	r.exec("UPDATE operations SET status = 'IN_PROGRESS', attempts = 1, first_attempt_at = now(), lease_token = $2, lease_until = now() + interval '30 seconds' WHERE id = $1",
		g.op, uuid.New())
	r.runOnce(false) // the lease still holds
	r.exec("UPDATE operations SET lease_until = now() - interval '1 second' WHERE id = $1", g.op)
	r.runOnce(true)
	r.requireState(g, "ACTIVE", "SUCCEEDED", 2)
}

// Worker A's lease expires mid-call; B re-claims and finishes; A's late answer is dropped.
func TestLateWorkerCannotOverwrite(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	r.faults.DelayNext(2 * time.Second)
	slow := access.NewRunner(r.db.App, access.NewHTTPToolClient(r.url, 5*time.Second))
	done := make(chan error, 1)
	go func() { _, err := slow.RunOnce(context.Background()); done <- err }()
	for deadline := time.Now().Add(5 * time.Second); r.count("SELECT count(*) FROM operations WHERE status = 'IN_PROGRESS'") == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("A never claimed")
		}
	}
	r.exec("UPDATE operations SET lease_until = now() - interval '1 second' WHERE id = $1", g.op)
	r.runOnce(true) // B
	r.requireState(g, "ACTIVE", "SUCCEEDED", 2)
	if err := <-done; err != nil {
		t.Fatalf("A: %v", err)
	}
	r.requireState(g, "ACTIVE", "SUCCEEDED", 2)
	if r.audits("GRANT_ACTIVATED") != 1 || r.held(g) != 1 {
		t.Fatalf("GRANT_ACTIVATED %d, permissions %d", r.audits("GRANT_ACTIVATED"), r.held(g))
	}
}

func TestConcurrentRunnersClaimEachOperationOnce(t *testing.T) {
	r := newRig(t)
	var grants []approvedGrant
	for range 20 {
		grants = append(grants, r.grant("API"))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			for {
				found, err := r.runner.RunOnce(context.Background())
				if err != nil {
					errs <- err
					return
				}
				if !found {
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for _, g := range grants {
		r.requireState(g, "ACTIVE", "SUCCEEDED", 1)
	}
	if n := r.faults.Requests(); n != 40 { // one call and one status lookup each
		t.Fatalf("mock requests = %d; want 40", n)
	}
}

func TestPriorityOrder(t *testing.T) {
	r := newRig(t)
	a, b, c, d := r.grant("API"), r.grant("API"), r.grant("API"), r.grant("API")
	// Removals first: termination or suspension (0), then others (1), then grants, oldest first.
	r.exec("UPDATE operations SET priority = 0 WHERE id = $1", d.op)
	r.exec("UPDATE operations SET priority = 1 WHERE id = $1", c.op)
	r.exec("UPDATE operations SET created_at = created_at - interval '1 hour' WHERE id = $1", b.op)
	want := []uuid.UUID{d.op, c.op, b.op, a.op}
	for _, op := range want {
		r.runOnce(true)
		if n := r.count("SELECT count(*) FROM operations WHERE id = $1 AND status = 'SUCCEEDED'", op); n != 1 {
			t.Fatalf("claim order: %v was not next", op)
		}
	}
}

// A removal waits while the grant's own provisioning call may still be in flight.
func TestRevokeWaitsForInFlightGrant(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	r.exec("UPDATE operations SET status = 'IN_PROGRESS', attempts = 1, first_attempt_at = now(), lease_token = $2, lease_until = now() + interval '30 seconds' WHERE id = $1",
		g.op, uuid.New())
	r.exec("UPDATE grants SET status = 'REVOCATION_REQUESTED' WHERE id = $1", g.id)
	r.exec("INSERT INTO operations (grant_id, type, reason, priority) VALUES ($1, 'REVOKE', 'VOLUNTARY', 1)", g.id)
	r.runOnce(false)
	r.exec("UPDATE operations SET lease_until = now() - interval '1 second' WHERE id = $1", g.op)
	r.runOnce(true) // the GRANT first, despite its lower priority
	if n := r.count("SELECT count(*) FROM operations WHERE id = $1 AND status = 'SUCCEEDED'", g.op); n != 1 {
		t.Fatal("the in-flight GRANT was not completed first")
	}
}

// An operation another worker is claiming is skipped, never waited for or claimed twice.
func TestClaimSkipsALockedOperation(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	ctx := context.Background()
	tx, err := r.db.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT 1 FROM operations WHERE id = $1 FOR UPDATE", g.op); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if found, err := r.runner.RunOnce(short); found || err != nil {
		t.Fatalf("RunOnce = %v, %v; want the locked operation skipped", found, err)
	}
}

// A retirement committed while the claim waited for the tool's lock is seen: the GRANT is
// cancelled, never sent.
func TestClaimSeesARetirementCommittedWhileItWaited(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	err := testutil.WhileLocked(t, r.db.App, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT 1 FROM tools WHERE id = $1 FOR UPDATE", g.tool); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE access_levels SET status = 'RETIRED' WHERE id = $1", g.level)
		return err
	}, func() error {
		_, err := r.runner.RunOnce(context.Background())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	r.requireState(g, "CANCELLED", "CANCELLED", 0)
	if r.faults.Requests() != 0 {
		t.Fatal("a retired level reached the tool")
	}
}

// A claim that cancels a retired, never-sent GRANT needs the employee (the audit event's key
// lock) after the operation's lock; a revoke holding the employee waits for that operation.
// The claim skips the operation while the employee is locked instead of deadlocking.
func TestClaimOfARetiredGrantYieldsToTheEmployeeLock(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	r.exec("UPDATE access_levels SET status = 'RETIRED' WHERE id = $1", g.level)
	ctx := context.Background()
	tx, err := r.db.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", g.emp.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := r.runner.RunOnce(ctx); done <- err }()
	select {
	case err := <-done: // skipped the operation while the employee was locked
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the claim waited for the employee while holding the operation")
	}
	if _, err := tx.Exec(ctx, "SELECT 1 FROM operations WHERE grant_id = $1 FOR UPDATE", g.id); err != nil {
		t.Fatalf("revoke's operation lock: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r.runOnce(true) // now the claim cancels it
	r.requireState(g, "CANCELLED", "CANCELLED", 0)
}

// The runner records under the employee's lock, taken before the operation's, like every
// other grant change (A9). Revoke locks the employee, then the operations: if record took
// the operation first, the two would deadlock.
func TestRunnerRecordsUnderTheEmployeeLock(t *testing.T) {
	r := newRig(t)
	g := r.grant("API")
	ctx := context.Background()
	tx, err := r.db.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", g.emp.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := r.runner.RunOnce(ctx); done <- err }()
	for deadline := time.Now().Add(5 * time.Second); r.count(`SELECT count(*) FROM pg_stat_activity
		WHERE datname = current_database() AND wait_event_type = 'Lock'`) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("record never waited for the employee")
		}
	}
	if _, err := tx.Exec(ctx, "SELECT 1 FROM operations WHERE grant_id = $1 FOR UPDATE", g.id); err != nil {
		t.Fatalf("revoke's operation lock: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	r.requireState(g, "ACTIVE", "SUCCEEDED", 1)
}
