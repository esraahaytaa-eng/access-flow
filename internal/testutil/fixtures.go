package testutil

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// EmployeeSpec describes a test employee; zero values get sensible defaults.
type EmployeeSpec struct {
	Email  string   // default: a unique address
	Status string   // default: ACTIVE
	Roles  []string // open role assignments
	// Password gives the employee the permanent password Password (hash PasswordHash).
	Password bool
}

// Password is the test password; PasswordHash is its argon2id hash, precomputed because
// testutil cannot import auth (auth's tests import testutil). auth tests check they match.
const (
	Password     = "correct horse battery"
	PasswordHash = "$argon2id$v=19$m=19456,t=2,p=1$QSOldEmzMi+hk5/lEvz57Q$NNDVDBoKQcdQ2tTzKEWG5eU5i3qFYdO0BsDHBiL+ocY"
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
	err := db.App.QueryRow(ctx, `INSERT INTO employees (email, full_name, status) VALUES ($1, $2, $3) RETURNING id`,
		spec.Email, "Test "+spec.Email, spec.Status).Scan(&e.ID)
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
