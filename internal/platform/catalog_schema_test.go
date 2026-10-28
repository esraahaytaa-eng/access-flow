package platform_test

import (
	"context"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestCatalogSchema(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	owner := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{})
	var group, tool, level string
	mustScan := func(sql string, dst *string, args ...any) {
		t.Helper()
		if err := db.App.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	mustScan("INSERT INTO admin_groups (name) VALUES ('IT') RETURNING id", &group)
	mustScan(`INSERT INTO tools (name, fulfillment_method, owner_id, admin_group_id) VALUES ('Jira', 'API', $1, $2) RETURNING id`,
		&tool, owner.ID, group)
	mustScan(`INSERT INTO access_levels (tool_id, name, requires_tool_owner, requires_infosec) VALUES ($1, 'Viewer', false, false) RETURNING id`,
		&level, tool)
	mustScan(`INSERT INTO starter_packages (department, access_level_id) VALUES ('Engineering', $1) RETURNING id`, new(string), level)

	cases := []struct {
		name, sql, state string
		args             []any
	}{
		{"method is one of the three", `INSERT INTO tools (name, fulfillment_method, owner_id, admin_group_id) VALUES ('X', 'SCRIPT', $1, $2)`, "23514", []any{owner.ID, group}},
		{"tool name is unique ignoring case", `INSERT INTO tools (name, fulfillment_method, owner_id, admin_group_id) VALUES ('jira', 'API', $1, $2)`, "23505", []any{owner.ID, group}},
		{"tool name is trimmed", `INSERT INTO tools (name, fulfillment_method, owner_id, admin_group_id) VALUES (' Y', 'API', $1, $2)`, "23514", []any{owner.ID, group}},
		{"level name is unique per tool", `INSERT INTO access_levels (tool_id, name, requires_tool_owner, requires_infosec) VALUES ($1, 'VIEWER', true, false)`, "23505", []any{tool}},
		{"status is known", `UPDATE tools SET status = 'PAUSED' WHERE id = $1`, "23514", []any{tool}},
		{"one open starter package entry", `INSERT INTO starter_packages (department, access_level_id) VALUES ('engineering', $1)`, "23505", []any{level}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.App.Exec(ctx, tc.sql, tc.args...)
			testutil.RequireSQLState(t, err, tc.state)
		})
	}

	// A removed entry no longer blocks a new one; a job-title entry is a different entry.
	if _, err := db.App.Exec(ctx, "UPDATE starter_packages SET removed_at = now()"); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`INSERT INTO starter_packages (department, access_level_id) VALUES ('engineering', $1)`,
		`INSERT INTO starter_packages (department, job_title, access_level_id) VALUES ('engineering', 'QA', $1)`,
	} {
		if _, err := db.App.Exec(ctx, sql, level); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	for _, table := range []string{"tools", "access_levels", "starter_packages", "notifications"} {
		t.Run("no delete on "+table, func(t *testing.T) {
			_, err := db.App.Exec(ctx, "DELETE FROM "+table)
			testutil.RequireSQLState(t, err, "42501")
		})
	}
}
