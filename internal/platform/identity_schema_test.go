package platform_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// Employees and their records are never physically deleted (A5); the runtime user has no DELETE.
func TestRuntimeUserCannotDeleteIdentityRecords(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	testutil.CreateEmployee(t, db, testutil.EmployeeSpec{Roles: []string{"INFOSEC"}})

	for _, table := range []string{"employees", "password_credentials", "role_assignments", "settings"} {
		t.Run(table, func(t *testing.T) {
			_, err := db.App.Exec(ctx, "DELETE FROM "+table)
			testutil.RequireSQLState(t, err, "42501")
		})
	}
}

func TestEmployeeConstraints(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	existing := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{Email: "ayse@example.com"})

	cases := []struct {
		name, sql, state string
		args             []any
	}{
		{"email must be stored lowercase", "INSERT INTO employees (email, full_name) VALUES ('Mixed@Example.com', 'X')", "23514", nil},
		{"email is unique", "INSERT INTO employees (email, full_name) VALUES ('ayse@example.com', 'X')", "23505", nil},
		{"status is one of the known values", "INSERT INTO employees (email, full_name, status) VALUES ('b@example.com', 'X', 'ON_LEAVE')", "23514", nil},
		{"name is required", "INSERT INTO employees (email, full_name) VALUES ('c@example.com', ' ')", "23514", nil},
		{"an employee cannot manage themselves", "UPDATE employees SET manager_id = id WHERE id = $1", "23514", []any{existing.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.App.Exec(ctx, tc.sql, tc.args...)
			testutil.RequireSQLState(t, err, tc.state)
		})
	}
}

func TestOnlyOneOpenAssignmentPerRole(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	e := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{Roles: []string{"AUDITOR"}})

	_, err := db.App.Exec(ctx, "INSERT INTO role_assignments (employee_id, role) VALUES ($1, 'AUDITOR')", e.ID)
	testutil.RequireSQLState(t, err, "23505")

	// Closing the interval allows a new one: history is kept as intervals.
	if _, err := db.App.Exec(ctx, "UPDATE role_assignments SET valid_to = now() WHERE employee_id = $1", e.ID); err != nil {
		t.Fatalf("close assignment: %v", err)
	}
	if _, err := db.App.Exec(ctx, "INSERT INTO role_assignments (employee_id, role) VALUES ($1, 'AUDITOR')", e.ID); err != nil {
		t.Fatalf("reassign after close: %v", err)
	}
	_, err = db.App.Exec(ctx, "INSERT INTO role_assignments (employee_id, role) VALUES ($1, 'JANITOR')", e.ID)
	testutil.RequireSQLState(t, err, "23514")
}

func TestPermanentPasswordsHaveNoExpiry(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	e := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{})

	_, err := db.App.Exec(ctx, `INSERT INTO password_credentials (employee_id, password_hash, is_temporary, expires_at)
		VALUES ($1, 'x', false, now())`, e.ID)
	testutil.RequireSQLState(t, err, "23514")
}

func TestAuditEventsReferenceRealEmployees(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)

	for _, column := range []string{"actor_id", "subject_employee_id"} {
		t.Run(column, func(t *testing.T) {
			_, err := db.App.Exec(ctx, "INSERT INTO audit_events ("+column+", action, entity_type) VALUES ($1, 'X', 'x')", uuid.New())
			testutil.RequireSQLState(t, err, "23503") // foreign_key_violation
		})
	}
}

// The CHECK mirrors auth.NormalizeEmail, so no code path can store an address the rule rejects.
func TestEmployeesEmailCheckMatchesTheGoRule(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	for _, email := range []string{"admİn@x.com", "ayşe@x.com", "a\tb@x.com", "a b@x.com", "nobody", "a@b@c",
		strings.Repeat("a", 64) + "@" + strings.Repeat("b", 186) + ".com"} {
		t.Run(email, func(t *testing.T) {
			_, err := db.App.Exec(ctx, "INSERT INTO employees (email, full_name) VALUES ($1, 'X')", email)
			testutil.RequireSQLState(t, err, "23514")
		})
	}
}

func TestAdminGroupSchema(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	emp := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{})
	var group uuid.UUID
	if err := db.App.QueryRow(ctx, "INSERT INTO admin_groups (name) VALUES ('Finance IT') RETURNING id").Scan(&group); err != nil {
		t.Fatal(err)
	}
	_, err := db.App.Exec(ctx, "INSERT INTO admin_groups (name) VALUES ('finance it')")
	testutil.RequireSQLState(t, err, "23505") // names are unique regardless of case
	if _, err := db.App.Exec(ctx, "INSERT INTO admin_group_memberships (group_id, employee_id) VALUES ($1, $2)", group, emp.ID); err != nil {
		t.Fatal(err)
	}
	_, err = db.App.Exec(ctx, "INSERT INTO admin_group_memberships (group_id, employee_id) VALUES ($1, $2)", group, emp.ID)
	testutil.RequireSQLState(t, err, "23505") // one open membership per pair
	for _, table := range []string{"admin_groups", "admin_group_memberships"} {
		_, err := db.App.Exec(ctx, "DELETE FROM "+table)
		testutil.RequireSQLState(t, err, "42501")
	}
}
