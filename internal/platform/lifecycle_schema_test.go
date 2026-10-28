package platform_test

import (
	"context"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// Suspension and termination (D7, A9): SUSPEND and RESUME operations, suspension
// intervals and scheduled terminations.
func TestLifecycleSchema(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	emp := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{})
	hr := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{})
	tool := testutil.CreateTool(t, db, testutil.ToolSpec{})
	level := testutil.CreateLevel(t, db, tool, "Viewer", false, false)
	var request, item, grant string
	mustScan := func(sql string, dst *string, args ...any) {
		t.Helper()
		if err := db.App.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	mustScan(`INSERT INTO requests (submission_id, beneficiary_id, submitted_by) VALUES (uuidv7(), $1, $1) RETURNING id`, &request, emp.ID)
	mustScan(`INSERT INTO request_items (request_id, beneficiary_id, tool_id, access_level_id, justification, duration_type, status)
		VALUES ($1, $2, $3, $4, 'x', 'UNTIL_REVOKED', 'APPROVED') RETURNING id`, &item, request, emp.ID, tool, level)
	mustScan(`INSERT INTO grants (request_item_id, employee_id, tool_id, access_level_id, method, status, activation_evidence)
		VALUES ($1, $2, $3, $4, 'API', 'SUSPENDED', 'VERIFIED') RETURNING id`, &grant, item, emp.ID, tool, level)
	for _, typ := range []string{"SUSPEND", "RESUME"} {
		if _, err := db.App.Exec(ctx, "INSERT INTO operations (grant_id, type, reason, priority, status) VALUES ($1, $2, 'SUSPENSION', 1, 'SUCCEEDED')",
			grant, typ); err != nil {
			t.Fatalf("%s operation: %v", typ, err)
		}
	}
	interval := "INSERT INTO grant_suspensions (grant_id, effective_from, evidence) VALUES ($1, now(), $2)"
	if _, err := db.App.Exec(ctx, interval, grant, "VERIFIED"); err != nil {
		t.Fatal(err)
	}

	suspendOp := "INSERT INTO operations (grant_id, type, reason, priority) VALUES ($1, 'SUSPEND', 'SUSPENSION', 1)"
	cases := []struct {
		name, setup, sql, state string
		args                    []any
	}{
		{"one open SUSPEND or RESUME per grant", suspendOp, "INSERT INTO operations (grant_id, type, reason, priority) VALUES ($1, 'RESUME', 'REACTIVATION', 2)", "23505", []any{grant}},
		{"operation type is known", "", "INSERT INTO operations (grant_id, type, reason, priority) VALUES ($1, 'PAUSE', 'X', 1)", "23514", []any{grant}},
		{"one open interval per grant", "", interval, "23505", []any{grant, "VERIFIED"}},
		{"interval evidence is known", "UPDATE grant_suspensions SET effective_to = now() WHERE grant_id = $1", interval, "23514", []any{grant, "IMPORTED"}},
		{"a schedule names who made it", "", "UPDATE employees SET termination_at = now() WHERE id = $1", "23514", []any{emp.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := db.App.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if tc.setup != "" {
				if _, err := tx.Exec(ctx, tc.setup, grant); err != nil {
					t.Fatal(err)
				}
			}
			_, err = tx.Exec(ctx, tc.sql, tc.args...)
			testutil.RequireSQLState(t, err, tc.state)
		})
	}

	if _, err := db.App.Exec(ctx, "UPDATE employees SET termination_at = now(), termination_scheduled_by = $2, termination_reason = 'leaving' WHERE id = $1",
		emp.ID, hr.ID); err != nil {
		t.Fatalf("schedule a termination: %v", err)
	}
	if _, err := db.App.Exec(ctx, "UPDATE grant_suspensions SET effective_to = now()"); err != nil {
		t.Fatalf("close an interval: %v", err)
	}
	_, err := db.App.Exec(ctx, "DELETE FROM grant_suspensions")
	testutil.RequireSQLState(t, err, "42501")
}
