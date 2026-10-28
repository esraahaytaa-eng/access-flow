package platform_test

import (
	"context"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestRequestsSchema(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	emp := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{})
	tool := testutil.CreateTool(t, db, testutil.ToolSpec{})
	viewer := testutil.CreateLevel(t, db, tool, "Viewer", false, false)
	admin := testutil.CreateLevel(t, db, tool, "Admin", true, true)
	var request, item string
	mustScan := func(sql string, dst *string, args ...any) {
		t.Helper()
		if err := db.App.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	mustScan(`INSERT INTO requests (submission_id, beneficiary_id, submitted_by) VALUES (uuidv7(), $1, $1) RETURNING id`, &request, emp.ID)
	newItem := `INSERT INTO request_items (request_id, beneficiary_id, tool_id, access_level_id, justification, duration_type, expires_on)
		VALUES ($1, $2, $3, $4, 'needed', $5, $6::date)`
	mustScan(newItem+" RETURNING id", &item, request, emp.ID, tool, viewer, "UNTIL_REVOKED", nil)

	cases := []struct {
		name, sql, state string
		args             []any
	}{
		{"submission ID is unique", `INSERT INTO requests (submission_id, beneficiary_id, submitted_by)
			SELECT submission_id, beneficiary_id, submitted_by FROM requests`, "23505", nil},
		{"temporary needs an expiry date", newItem, "23514", []any{request, emp.ID, tool, admin, "TEMPORARY", nil}},
		{"until revoked has no expiry date", newItem, "23514", []any{request, emp.ID, tool, admin, "UNTIL_REVOKED", "2030-01-01"}},
		{"one open item per person and tool, whatever the level", newItem, "23505", []any{request, emp.ID, tool, admin, "UNTIL_REVOKED", nil}},
		{"item status is known", `UPDATE request_items SET status = 'PAUSED' WHERE id = $1`, "23514", []any{item}},
		{"a pending step has an assignee or the pool", `INSERT INTO approval_steps (item_id, position, type, status) VALUES ($1, 1, 'MANAGER', 'PENDING')`, "23514", []any{item}},
		{"a step has one position per item", `INSERT INTO approval_steps (item_id, position, type) VALUES ($1, 1, 'MANAGER'), ($1, 1, 'INFOSEC')`, "23505", []any{item}},
		{"the copied tool is the level's tool", newItem, "23503", []any{request, emp.ID, testutil.CreateTool(t, db, testutil.ToolSpec{}), admin, "UNTIL_REVOKED", nil}},
		{"the copied beneficiary is the request's", newItem, "23503", []any{request, testutil.CreateEmployee(t, db, testutil.EmployeeSpec{}).ID, tool, admin, "UNTIL_REVOKED", nil}},
		{"one email per submission", `INSERT INTO email_outbox (recipient_id, request_id, subject, body) VALUES ($1, $2, 's', 'b'), ($1, $2, 's', 'b')`, "23505", []any{emp.ID, request}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.App.Exec(ctx, tc.sql, tc.args...)
			testutil.RequireSQLState(t, err, tc.state)
		})
	}

	// A closed item no longer blocks a new request for the tool.
	if _, err := db.App.Exec(ctx, "UPDATE request_items SET status = 'REJECTED' WHERE id = $1", item); err != nil {
		t.Fatal(err)
	}
	if _, err := db.App.Exec(ctx, newItem, request, emp.ID, tool, admin, "TEMPORARY", "2030-01-01"); err != nil {
		t.Fatalf("new item after rejection: %v", err)
	}

	for _, table := range []string{"requests", "request_items", "approval_steps", "email_outbox"} {
		t.Run("no delete on "+table, func(t *testing.T) {
			_, err := db.App.Exec(ctx, "DELETE FROM "+table)
			testutil.RequireSQLState(t, err, "42501")
		})
	}
	for _, table := range []string{"requests", "email_outbox"} {
		t.Run("no update on "+table, func(t *testing.T) {
			_, err := db.App.Exec(ctx, "UPDATE "+table+" SET created_at = now()")
			testutil.RequireSQLState(t, err, "42501")
		})
	}
}
