package platform_test

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// Migration 15 turns the free-text departments and job titles in use into catalog entries
// before it adds the foreign keys, so no existing row can fail them.
func TestCompanyCatalogMigrationKeepsExistingValues(t *testing.T) {
	ctx := context.Background()
	db, migrate := testutil.NewDBAt(t, 14)
	var level string
	for _, sql := range []string{
		`INSERT INTO employees (email, full_name, department, job_title) VALUES
			('a@x.test', 'A', 'Engineering', 'Backend Developer'),
			('b@x.test', 'B', 'engineering', NULL),
			('c@x.test', 'C', NULL, 'Kıdemli Geliştirici')`,
		`INSERT INTO admin_groups (name) VALUES ('IT')`,
		`INSERT INTO tools (name, fulfillment_method, owner_id, admin_group_id)
			SELECT 'Jira', 'MANUAL', (SELECT id FROM employees WHERE email = 'a@x.test'), id FROM admin_groups`,
	} {
		if _, err := db.Owner.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	if err := db.Owner.QueryRow(ctx, `INSERT INTO access_levels (tool_id, name, requires_tool_owner, requires_infosec)
		SELECT id, 'User', false, false FROM tools RETURNING id`).Scan(&level); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Owner.Exec(ctx, `INSERT INTO starter_packages (department, job_title, access_level_id) VALUES ('Sales', 'QA', $1)`, level); err != nil {
		t.Fatal(err)
	}

	// Grants from earlier imports: the tool read observed its grants in the tool; a CSV only reported them.
	for _, source := range []string{"MOCK", "CSV"} {
		if _, err := db.Owner.Exec(ctx, `WITH i AS (
				INSERT INTO imports (source, file_name, imported_by, imported_at, report)
				SELECT $1, CASE WHEN $1 = 'CSV' THEN 'old.csv' END, id, '2026-10-01T09:00:00Z', '{}' FROM employees WHERE email = 'a@x.test' RETURNING id)
			INSERT INTO grants (tool_id, access_level_id, method, status, import_id, account)
			SELECT tools.id, $2, 'MANUAL', 'ACTIVE', i.id, lower($1) || '@x.test' FROM tools, i`, source, level); err != nil {
			t.Fatal(err)
		}
	}

	migrate()

	for account, want := range map[string]bool{"mock@x.test": true, "csv@x.test": false} {
		var verified bool
		if err := db.App.QueryRow(ctx, `SELECT g.tool_verified_at IS NOT DISTINCT FROM i.imported_at AND g.tool_verified_at IS NOT NULL
			FROM grants g JOIN imports i ON i.id = g.import_id WHERE g.account = $1`, account).Scan(&verified); err != nil {
			t.Fatal(err)
		}
		if verified != want {
			t.Errorf("%s verified at its import = %v, want %v", account, verified, want)
		}
	}

	names := func(table string) []string {
		rows, err := db.App.Query(ctx, "SELECT name FROM "+table+" WHERE active AND created_by IS NULL ORDER BY name")
		if err != nil {
			t.Fatal(err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	// Spellings that differ only by case were both in use; both are kept, nothing is rewritten.
	if got, want := names("departments"), []string{"Engineering", "Sales", "engineering"}; !slices.Equal(got, want) {
		t.Errorf("departments = %v, want %v", got, want)
	}
	if got, want := names("job_positions"), []string{"Backend Developer", "Kıdemli Geliştirici", "QA"}; !slices.Equal(got, want) {
		t.Errorf("job positions = %v, want %v", got, want)
	}
}

func TestOnboardingSchema(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	owner := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{})
	tool := testutil.CreateTool(t, db, testutil.ToolSpec{OwnerID: owner.ID})
	var level string
	if err := db.App.QueryRow(ctx, `INSERT INTO access_levels (tool_id, name, requires_tool_owner, requires_infosec)
		VALUES ($1, 'User', false, false) RETURNING id`, tool).Scan(&level); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"INSERT INTO departments (name) VALUES ('Engineering')",
		"INSERT INTO job_positions (name) VALUES ('Backend Developer')",
		"INSERT INTO job_positions (name, active) VALUES ('Old Title', false)",
	} {
		if _, err := db.App.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	cases := []struct {
		name, sql, state string
		args             []any
	}{
		{"employee department must be defined", `UPDATE employees SET department = 'Marketing' WHERE id = $1`, "23503", []any{owner.ID}},
		{"employee job position must be defined", `UPDATE employees SET job_title = 'CTO' WHERE id = $1`, "23503", []any{owner.ID}},
		{"starter package department must be defined", `INSERT INTO starter_packages (department, access_level_id) VALUES ('Marketing', $1)`, "23503", []any{level}},
		{"name is exact and unique", `INSERT INTO departments (name) VALUES ('Engineering')`, "23505", nil},
		{"name is trimmed", `INSERT INTO departments (name) VALUES (' HR')`, "23514", nil},
		{"name is not empty", `INSERT INTO job_positions (name) VALUES ('')`, "23514", nil},
		{"a workbook import names its file", `INSERT INTO imports (source, imported_by, imported_at, report) VALUES ('WORKBOOK', $1, now(), '{}')`, "23514", []any{owner.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.App.Exec(ctx, tc.sql, tc.args...)
			testutil.RequireSQLState(t, err, tc.state)
		})
	}

	// A retired entry stays valid for the rows that hold it (history is kept).
	if _, err := db.App.Exec(ctx, `UPDATE employees SET department = 'Engineering', job_title = 'Old Title' WHERE id = $1`, owner.ID); err != nil {
		t.Fatalf("retired value: %v", err)
	}
	if _, err := db.App.Exec(ctx, `INSERT INTO imports (source, file_name, imported_by, imported_at, report)
		VALUES ('WORKBOOK', 'onboarding.xlsx', $1, now(), '{}')`, owner.ID); err != nil {
		t.Fatalf("workbook import: %v", err)
	}
	if _, err := db.App.Exec(ctx, "UPDATE grants SET tool_verified_at = now() WHERE false"); err != nil {
		t.Fatalf("tool_verified_at: %v", err)
	}
	for _, table := range []string{"departments", "job_positions"} {
		_, err := db.App.Exec(ctx, "DELETE FROM "+table)
		testutil.RequireSQLState(t, err, "42501")
	}
}
