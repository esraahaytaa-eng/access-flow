package testutil

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// EmployeeSpec describes a test employee; zero values get sensible defaults.
type EmployeeSpec struct {
	Email                string    // default: a unique address
	Status               string    // default: ACTIVE
	Roles                []string  // open role assignments
	Department, JobTitle string    // default: none
	ManagerID            uuid.UUID // default: none
	// Password gives the employee the permanent password Password (hash PasswordHash).
	Password bool
}

// Password is the test password; PasswordHash is its argon2id hash, precomputed because
// testutil cannot import auth (auth's tests import testutil). auth tests check they match.
// The hash carries minimal cost parameters (64 KiB, 1 pass), which VerifyPassword reads from
// it: a 19 MiB verification per sign-in made parallel -race tests memory-bound. Production
// hashes keep the OWASP parameters (A4; auth's HashPassword test pins them).
const (
	Password     = "correct horse battery"
	PasswordHash = "$argon2id$v=19$m=64,t=1,p=1$YWNjZXNzZmxvdy10ZXN0cw$lhLFv+nC5exVpH4+cZ3EnH1AVHcEug4HBY1nSzoMG/Q"
)

// Employee is a created test employee.
type Employee struct {
	ID    uuid.UUID
	Email string
}

// CreateEmployee inserts an employee as the runtime user, as the application would.
func CreateEmployee(t testing.TB, db DB, spec EmployeeSpec) Employee {
	t.Helper()
	ctx := context.Background()
	if spec.Email == "" {
		spec.Email = "user-" + randomHex(t) + "@example.com"
	}
	if spec.Status == "" {
		spec.Status = "ACTIVE"
	}
	e := Employee{Email: spec.Email}
	DefineDepartments(t, db, spec.Department)
	DefinePositions(t, db, spec.JobTitle)
	var manager *uuid.UUID
	if spec.ManagerID != uuid.Nil {
		manager = &spec.ManagerID
	}
	err := db.App.QueryRow(ctx, `INSERT INTO employees (email, full_name, status, department, job_title, manager_id)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6) RETURNING id`,
		spec.Email, "Test "+spec.Email, spec.Status, spec.Department, spec.JobTitle, manager).Scan(&e.ID)
	if err != nil {
		t.Fatalf("create employee %s: %v", spec.Email, err)
	}
	if spec.Password {
		if _, err := db.App.Exec(ctx, "INSERT INTO password_credentials (employee_id, password_hash, is_temporary) VALUES ($1, $2, false)",
			e.ID, PasswordHash); err != nil {
			t.Fatalf("set password: %v", err)
		}
	}
	for _, role := range spec.Roles {
		if _, err := db.App.Exec(ctx, "INSERT INTO role_assignments (employee_id, role) VALUES ($1, $2)", e.ID, role); err != nil {
			t.Fatalf("assign %s: %v", role, err)
		}
	}
	return e
}

// DefineDepartments adds active catalog departments (M17); existing and empty names are skipped.
func DefineDepartments(t testing.TB, db DB, names ...string) { define(t, db, "departments", names) }

// DefinePositions adds active catalog job positions; existing and empty names are skipped.
func DefinePositions(t testing.TB, db DB, names ...string) { define(t, db, "job_positions", names) }

func define(t testing.TB, db DB, table string, names []string) {
	t.Helper()
	for _, n := range names {
		if n == "" {
			continue
		}
		if _, err := db.App.Exec(context.Background(), "INSERT INTO "+table+" (name) VALUES ($1) ON CONFLICT (name) DO NOTHING", n); err != nil {
			t.Fatalf("define %s %q: %v", table, n, err)
		}
	}
}

// CreateGroup inserts an admin group with a unique name.
func CreateGroup(t testing.TB, db DB) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.App.QueryRow(context.Background(), "INSERT INTO admin_groups (name) VALUES ($1) RETURNING id",
		"group-"+randomHex(t)).Scan(&id); err != nil {
		t.Fatalf("create group: %v", err)
	}
	return id
}

// ToolSpec describes a test tool; zero values get a unique name, API, a new owner and group.
type ToolSpec struct {
	Name, Method string
	OwnerID      uuid.UUID
}

// CreateTool inserts an active tool.
func CreateTool(t testing.TB, db DB, spec ToolSpec) uuid.UUID {
	t.Helper()
	if spec.Name == "" {
		spec.Name = "tool-" + randomHex(t)
	}
	if spec.Method == "" {
		spec.Method = "API"
	}
	if spec.OwnerID == uuid.Nil {
		spec.OwnerID = CreateEmployee(t, db, EmployeeSpec{}).ID
	}
	var id uuid.UUID
	if err := db.App.QueryRow(context.Background(), `INSERT INTO tools (name, fulfillment_method, owner_id, admin_group_id)
		VALUES ($1, $2, $3, $4) RETURNING id`, spec.Name, spec.Method, spec.OwnerID, CreateGroup(t, db)).Scan(&id); err != nil {
		t.Fatalf("create tool: %v", err)
	}
	return id
}

// CreateLevel inserts an active access level with the given optional steps.
func CreateLevel(t testing.TB, db DB, tool uuid.UUID, name string, toolOwner, infosec bool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.App.QueryRow(context.Background(), `INSERT INTO access_levels (tool_id, name, requires_tool_owner, requires_infosec)
		VALUES ($1, $2, $3, $4) RETURNING id`, tool, name, toolOwner, infosec).Scan(&id); err != nil {
		t.Fatalf("create level: %v", err)
	}
	return id
}
