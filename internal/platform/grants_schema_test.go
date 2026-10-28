package platform_test

import (
	"context"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestGrantsSchema(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	emp := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{})
	tool := testutil.CreateTool(t, db, testutil.ToolSpec{})
	level := testutil.CreateLevel(t, db, tool, "Viewer", false, false)
	var request, item, item2, grant string
	mustScan := func(sql string, dst *string, args ...any) {
		t.Helper()
		if err := db.App.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	mustScan(`INSERT INTO requests (submission_id, beneficiary_id, submitted_by) VALUES (uuidv7(), $1, $1) RETURNING id`, &request, emp.ID)
	newItem := `INSERT INTO request_items (request_id, beneficiary_id, tool_id, access_level_id, justification, duration_type, status)
		VALUES ($1, $2, $3, $4, 'x', 'UNTIL_REVOKED', 'APPROVED') RETURNING id`
	mustScan(newItem, &item, request, emp.ID, tool, level)
	mustScan(newItem, &item2, request, emp.ID, tool, level)
	newGrant := `INSERT INTO grants (request_item_id, employee_id, tool_id, access_level_id, method, status, activation_evidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`
	mustScan(newGrant, &grant, item, emp.ID, tool, level, "API", "PROVISIONING", nil)
	if _, err := db.App.Exec(ctx, "INSERT INTO operations (grant_id, type, reason, priority) VALUES ($1, 'GRANT', 'APPROVAL', 2)", grant); err != nil {
		t.Fatal(err)
	}

	open := []any{item2, emp.ID, tool, level, "API", "PROVISIONING", nil}
	cases := []struct {
		name, setup, sql, state string
		args                    []any
	}{
		{"one grant per approved item", "", newGrant, "23505", []any{item, emp.ID, tool, level, "API", "PROVISIONING", nil}},
		{"one open grant per person and level", "", newGrant, "23505", open},
		{"a failed grant is still open", "UPDATE grants SET status = 'FAILED'", newGrant, "23505", open},
		{"verified evidence only for API", "", newGrant, "23514", []any{item2, emp.ID, tool, level, "MANUAL", "ACTIVE", "VERIFIED"}},
		{"grant status is known", "", "UPDATE grants SET status = 'PAUSED'", "23514", nil},
		{"item may be cancelled, not paused", "", "UPDATE request_items SET status = 'PAUSED'", "23514", nil},
		{"one grant operation per grant", "", "INSERT INTO operations (grant_id, type, reason, priority) VALUES ($1, 'GRANT', 'APPROVAL', 2)", "23505", []any{grant}},
		{"one open removal per grant", "", "INSERT INTO operations (grant_id, type, reason, priority) VALUES ($1, 'REVOKE', 'VOLUNTARY', 1), ($1, 'REVOKE', 'VOLUNTARY', 1)", "23505", []any{grant}},
		{"priority is 0 to 2", "", "INSERT INTO operations (grant_id, type, reason, priority) VALUES ($1, 'REVOKE', 'VOLUNTARY', 7)", "23514", []any{grant}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := db.App.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if tc.setup != "" {
				if _, err := tx.Exec(ctx, tc.setup); err != nil {
					t.Fatal(err)
				}
			}
			_, err = tx.Exec(ctx, tc.sql, tc.args...)
			testutil.RequireSQLState(t, err, tc.state)
		})
	}

	if _, err := db.App.Exec(ctx, "UPDATE request_items SET status = 'CANCELLED', closed_reason = 'access level retired' WHERE id = $1", item2); err != nil {
		t.Fatalf("cancel an item: %v", err)
	}
	// A revoked grant no longer blocks a new one for the level.
	if _, err := db.App.Exec(ctx, "UPDATE grants SET status = 'REVOKED'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.App.Exec(ctx, newGrant, item2, emp.ID, tool, level, "API", "PROVISIONING", nil); err != nil {
		t.Fatalf("grant after revocation: %v", err)
	}
	for _, table := range []string{"grants", "operations"} {
		t.Run("no delete on "+table, func(t *testing.T) {
			_, err := db.App.Exec(ctx, "DELETE FROM "+table)
			testutil.RequireSQLState(t, err, "42501")
		})
	}
}
