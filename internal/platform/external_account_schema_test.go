package platform_test

import (
	"context"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// A tool-specific external account (DECISIONS.md A1 note 2026-11-01) is fixed once recorded:
// the tool's account kind, and the account and its numeric reference on items and grants.
func TestExternalAccountSchema(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	emp := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{})
	tool := testutil.CreateTool(t, db, testutil.ToolSpec{AccountKind: "GITHUB_LOGIN"})
	level := testutil.CreateLevel(t, db, tool, "Read", false, false)
	var request, item, grant string
	mustScan := func(sql string, dst *string, args ...any) {
		t.Helper()
		if err := db.App.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	mustScan(`INSERT INTO requests (submission_id, beneficiary_id, submitted_by) VALUES (uuidv7(), $1, $1) RETURNING id`, &request, emp.ID)
	mustScan(`INSERT INTO request_items (request_id, beneficiary_id, tool_id, access_level_id, justification, duration_type, status, account, account_ref)
		VALUES ($1, $2, $3, $4, 'x', 'UNTIL_REVOKED', 'APPROVED', 'octocat', '583231') RETURNING id`, &item, request, emp.ID, tool, level)
	mustScan(`INSERT INTO grants (request_item_id, employee_id, tool_id, access_level_id, method, status, account, account_ref)
		VALUES ($1, $2, $3, $4, 'API', 'PROVISIONING', 'octocat', '583231') RETURNING id`, &grant, item, emp.ID, tool, level)

	cases := []struct{ name, sql, state string }{
		{"account kind is known", "INSERT INTO tools (name, fulfillment_method, owner_id, admin_group_id, account_kind) SELECT 'x', 'API', owner_id, admin_group_id, 'SLACK_ID' FROM tools", "23514"},
		{"account kind is fixed", "UPDATE tools SET account_kind = NULL", "P0001"},
		{"item account is fixed", "UPDATE request_items SET account = 'someone-else'", "P0001"},
		{"item account reference is fixed", "UPDATE request_items SET account_ref = '1'", "P0001"},
		{"grant account is fixed", "UPDATE grants SET account = 'someone-else'", "P0001"},
		{"grant account reference is fixed", "UPDATE grants SET account_ref = '1'", "P0001"},
		{"a reference needs an account", `INSERT INTO request_items (request_id, beneficiary_id, tool_id, access_level_id, justification, duration_type, account_ref)
			SELECT request_id, beneficiary_id, tool_id, access_level_id, 'x', 'UNTIL_REVOKED', '1' FROM request_items`, "23514"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := db.App.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			_, err = tx.Exec(ctx, tc.sql)
			testutil.RequireSQLState(t, err, tc.state)
		})
	}

	// Other changes to the same rows still work.
	if _, err := db.App.Exec(ctx, "UPDATE grants SET status = 'ACTIVE' WHERE id = $1", grant); err != nil {
		t.Fatalf("status change on a grant with an account: %v", err)
	}
	if _, err := db.App.Exec(ctx, "UPDATE tools SET name = name || ' (renamed)' WHERE id = $1", tool); err != nil {
		t.Fatalf("rename a tool with an account kind: %v", err)
	}
}
