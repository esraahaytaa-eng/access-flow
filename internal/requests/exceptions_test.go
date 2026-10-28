package requests

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// failed gives dev a Jira Viewer grant whose GRANT operation the tool rejected (409).
func (w *world) failed(tl *tools) {
	w.t.Helper()
	w.approved(w.jiraViewer)
	tl.faults.RespondNext(http.StatusConflict, `{"error":"level_suspended"}`)
	tl.drain(w.t)
}

func (w *world) exceptions(p person) testutil.Response {
	return w.get(p.c, "/api/v1/exceptions")
}

// failedOp is the only failed operation in Exceptions.
func (w *world) failedOp(p person) map[string]any {
	w.t.Helper()
	r := w.exceptions(p)
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	ops := r.Body["failedOperations"].([]any)
	if len(ops) != 1 {
		w.t.Fatalf("failed operations = %v", ops)
	}
	return ops[0].(map[string]any)
}

func retry(p person, op map[string]any) testutil.Response {
	return p.c.Do(http.MethodPost, "/api/v1/operations/"+op["id"].(string)+"/retry", map[string]any{"version": op["version"]})
}

// member adds p to the admin group of tool.
func (w *world) member(p person, tool uuid.UUID) {
	w.exec("INSERT INTO admin_group_memberships (group_id, employee_id) SELECT admin_group_id, $2 FROM tools WHERE id = $1", tool, p.ID)
}

func TestExceptionsListFailedOperationsAndUnroutableSteps(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	w.failed(tl)
	// is2 has no manager: the step goes to the InfoSec pool, where is2 is excluded and is1 has left.
	w.mustSubmit(w.is2.c, body(nil, item(w.awsAdmin)))
	w.exec("UPDATE role_assignments SET valid_to = now() WHERE employee_id = $1", w.is1.ID)
	auditor := w.person(testutil.EmployeeSpec{Roles: []string{"AUDITOR"}})

	for _, p := range []person{w.is2, w.admin, auditor} {
		op := w.failedOp(p)
		if op["type"] != "GRANT" || op["tool"] != "Jira" || op["level"] != "Viewer" || op["attempts"] != float64(1) ||
			op["lastError"] == "" || op["failedAt"] == nil || op["employee"].(map[string]any)["id"] != w.dev.ID.String() {
			t.Fatalf("failed operation = %v", op)
		}
		steps := w.exceptions(p).Body["unroutableSteps"].([]any)
		if len(steps) != 1 || steps[0].(map[string]any)["item"].(map[string]any)["beneficiary"].(map[string]any)["id"] != w.is2.ID.String() {
			t.Fatalf("unroutable steps = %v", steps)
		}
	}
	for _, p := range []person{w.dev, w.mgr, w.owner, w.is1} {
		testutil.RequireStatus(t, w.exceptions(p), http.StatusForbidden, "")
	}
}

func TestManualRetryWorks(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	// The real mock rejects the GRANT: dev's level is suspended in the tool (409 level_suspended).
	w.approved(w.jiraViewer)
	suspended := func(sql string) {
		if _, err := tl.db.Exec(context.Background(), sql, w.jira.String(), w.dev.Email, w.jiraViewer.String()); err != nil {
			t.Fatal(err)
		}
	}
	suspended("INSERT INTO accounts (tool, account) SELECT $1, $2 WHERE $3::text IS NOT NULL")
	suspended("INSERT INTO permissions (tool, account, level, status) VALUES ($1, $2, $3, 'SUSPENDED')")
	tl.drain(t)
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "FAILED", "FAILED")
	suspended("DELETE FROM permissions WHERE tool = $1 AND account = $2 AND level = $3") // the tool's admin fixed it
	fixer := w.person(testutil.EmployeeSpec{})
	w.member(fixer, w.jira)
	op := w.failedOp(w.admin)
	r := retry(fixer, op)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["status"] != "PENDING" || r.Body["attempts"] != float64(0) || r.Body["id"] != op["id"] {
		t.Fatalf("retried = %v", r.Body)
	}
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "PROVISIONING", "PROVISIONING")
	tl.drain(t)
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "ACTIVE", "ACTIVE / VERIFIED")
	// The M4 follow-up: the mock stores only applied operations, so the same ID is evaluated again.
	var stored int
	if err := tl.db.QueryRow(context.Background(), "SELECT count(*) FROM operations WHERE operation_id = $1", op["id"]).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("mock operations with the same ID = %d %v", stored, err)
	}
	if n := w.count("SELECT count(*) FROM audit_events WHERE action = 'OPERATION_RETRIED' AND actor_id = $1", fixer.ID); n != 1 {
		t.Fatalf("OPERATION_RETRIED = %d", n)
	}
	if ops := w.exceptions(w.admin).Body["failedOperations"].([]any); len(ops) != 0 {
		t.Fatalf("still in Exceptions: %v", ops)
	}
}

func TestRetryOfAFailedRemoval(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.jiraViewer))
	tl.faults.RespondNext(http.StatusConflict, `{"error":"operation_id_reused"}`)
	tl.drain(t)
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "REVOCATION_FAILED", "REVOCATION_FAILED") // still held (O1)
	op := w.failedOp(w.admin)
	testutil.RequireStatus(t, retry(w.admin, op), http.StatusOK, "")
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
	tl.drain(t)
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "REVOKED", "REVOKED / VERIFIED")
}

func TestRetryPermissionMatrix(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	w.failed(tl)
	auditor := w.person(testutil.EmployeeSpec{Roles: []string{"AUDITOR"}})
	other := w.person(testutil.EmployeeSpec{})
	w.member(other, testutil.CreateTool(t, w.db, testutil.ToolSpec{OwnerID: w.owner.ID})) // another tool's group
	op := w.failedOp(w.admin)
	for _, p := range []person{auditor, other, w.is2, w.owner, w.mgr} {
		testutil.RequireStatus(t, retry(p, op), http.StatusForbidden, "")
	}
	w.member(w.dev, w.jira) // the beneficiary never fulfils their own item (F2)
	testutil.RequireStatus(t, retry(w.dev, op), http.StatusForbidden, "separation_of_duties")
	testutil.RequireStatus(t, retry(w.admin, map[string]any{"id": uuid.NewString(), "version": 1}), http.StatusNotFound, "")
	stale := map[string]any{"id": op["id"], "version": op["version"].(float64) - 1}
	testutil.RequireStatus(t, retry(w.admin, stale), http.StatusConflict, "version_conflict")
	if w.audits("OPERATION_RETRIED") != 0 {
		t.Fatal("a refused retry was recorded")
	}
}

func TestRetryIsIdempotent(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	w.failed(tl)
	op := w.failedOp(w.admin)
	for range 2 {
		r := retry(w.admin, op) // the second with the older version
		testutil.RequireStatus(t, r, http.StatusOK, "")
		if r.Body["status"] != "PENDING" {
			t.Fatalf("retry = %v", r.Body)
		}
	}
	if w.audits("OPERATION_RETRIED") != 1 {
		t.Fatalf("OPERATION_RETRIED = %d", w.audits("OPERATION_RETRIED"))
	}
	// A cancelled operation is not retried.
	w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.jiraViewer))
	testutil.RequireStatus(t, retry(w.admin, op), http.StatusConflict, "operation_not_failed")
}
