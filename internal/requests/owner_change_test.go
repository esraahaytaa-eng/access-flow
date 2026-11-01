package requests

import (
	"context"
	"net/http"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// A tool owner change moves the tool's pending Tool Owner steps to the new owner, as it moves
// review items (D8 note 2026-10-31): the former owner can no longer decide them.
func TestOwnerChangeReroutesPendingToolOwnerSteps(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	req := w.mustSubmit(w.dev.c, body(nil, item(w.jiraAdmin))) // Manager, Tool Owner, InfoSec
	it := w.approve(w.mgr.c, open(t, items(req)[0]))
	ownerStep := open(t, it)
	if ownerStep["type"] != "TOOL_OWNER" || ownerStep["assigneeId"] != w.owner.ID.String() {
		t.Fatalf("step = %v", ownerStep)
	}

	heir := w.person(testutil.EmployeeSpec{})
	var group string
	if err := w.db.App.QueryRow(context.Background(), "SELECT admin_group_id::text FROM tools WHERE id = $1", w.jira).Scan(&group); err != nil {
		t.Fatal(err)
	}
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodPut, "/api/v1/tools/"+w.jira.String(),
		map[string]any{"name": "Jira", "ownerId": heir.ID, "adminGroupId": group, "version": 1}), http.StatusOK, "")

	r := w.get(w.dev.c, "/api/v1/requests/"+req["id"].(string))
	testutil.RequireStatus(t, r, http.StatusOK, "")
	moved := open(t, items(r.Body)[0])
	if moved["id"] != ownerStep["id"] || moved["assigneeId"] != heir.ID.String() {
		t.Fatalf("after the owner change: %v", moved)
	}
	testutil.RequireStatus(t, decide(w.owner.c, moved, "approve", nil), http.StatusForbidden, "forbidden")
	w.approve(heir.c, moved)
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'STEP_REROUTED' AND entity_id = $1 AND reason = 'tool owner changed'`,
		ownerStep["id"]); n != 1 {
		t.Fatalf("STEP_REROUTED events = %d", n)
	}
}
